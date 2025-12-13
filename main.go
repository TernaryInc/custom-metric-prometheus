package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/s3blob"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/spf13/cobra"
)

var (
	awsBucketRegion  string
	destination      string
	k8sClusterID     string
	metrics          []string
	prefix           string
	prometheusURL    string
	referenceTimeStr string
)

const (
	reservedColumnChargePeriodStart = "ChargePeriodStart" // reserved column name for Ternary BYOD
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "custom-metric-prometheus",
		Short: "A tool to fetch Prometheus metrics, convert them to CSV format, and deposit in blob storage.",
		RunE:  run,
	}

	rootCmd.Flags().StringSliceVar(&metrics, "metrics", []string{}, "List of metrics to fetch (required)")
	rootCmd.Flags().StringVar(&awsBucketRegion, "aws-bucket-region", "us-east-1", "If using an S3 destination bucket, specify the region")
	rootCmd.Flags().StringVar(&destination, "dest", "", "Destination URL e.g. s3://bucket (required)")
	rootCmd.Flags().StringVar(&k8sClusterID, "k8s-cluster-id", "", "Unique kubernetes cluster ID to disambiguate the output metrics file (required)")
	rootCmd.Flags().StringVar(&prefix, "prefix", "", "Path prefix to write within bucket (required)")
	rootCmd.Flags().StringVar(&prometheusURL, "prometheus-url", "", "Base URL of Prometheus server (required)")
	rootCmd.Flags().StringVar(&referenceTimeStr, "reference-time", "", "Metrics export as of this date YYYY-MM-DD; current UTC+0 date by default (optional)")

	// Mark required flags
	if err := rootCmd.MarkFlagRequired("metrics"); err != nil {
		log.Fatalf("metrics flag missing")
	}
	if err := rootCmd.MarkFlagRequired("dest"); err != nil {
		log.Fatalf("dest flag missing")
	}
	if err := rootCmd.MarkFlagRequired("k8s-cluster-id"); err != nil {
		log.Fatalf("k8s-cluster-id flag missing")
	}
	if err := rootCmd.MarkFlagRequired("prefix"); err != nil {
		log.Fatalf("prefix flag missing")
	}
	if err := rootCmd.MarkFlagRequired("prometheus-url"); err != nil {
		log.Fatalf("prometheus-url flag missing")
	}

	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("execute root command: %v", err)
		os.Exit(1)
	}
}

func buildOpenBucketURL(dest, region, prefix string) (string, error) {
	// Parse destination URL
	destURL, err := url.Parse(dest)
	if err != nil {
		return "", fmt.Errorf("parse destination URL: %w", err)
	}

	// Build query parameters
	query := destURL.Query()
	query.Set("region", region)
	query.Set("awssdk", "v2")
	if prefix != "" {
		// Normalize prefix to always end with exactly one slash
		// TrimRight removes all trailing slashes, then we add one back
		normalizedPrefix := strings.TrimRight(prefix, "/") + "/"
		query.Set("prefix", normalizedPrefix)
	}
	destURL.RawQuery = query.Encode()

	return destURL.String(), nil
}

func run(cmd *cobra.Command, args []string) error {
	// Validate that required flags have non-empty values
	// (MarkFlagRequired only checks if flags are provided, not if they're non-empty)
	if len(metrics) == 0 {
		return fmt.Errorf("at least one metric must be specified with --metrics")
	}
	if destination == "" {
		return fmt.Errorf("--dest cannot be empty")
	}
	if k8sClusterID == "" {
		return fmt.Errorf("--k8s-cluster-id cannot be empty")
	}
	if prefix == "" {
		return fmt.Errorf("--prefix cannot be empty")
	}
	if prometheusURL == "" {
		return fmt.Errorf("--prometheus-url cannot be empty")
	}

	// Initialize Prometheus client
	promHTTPClient, err := api.NewClient(api.Config{Address: prometheusURL})
	if err != nil {
		return fmt.Errorf("create Prometheus client: %v", err)
	}

	promAPI := v1.NewAPI(promHTTPClient)

	bucketURL, err := buildOpenBucketURL(destination, awsBucketRegion, prefix)
	if err != nil {
		return fmt.Errorf("build open bucket URL: %w", err)
	}

	bucket, err := blob.OpenBucket(cmd.Context(), bucketURL)
	if err != nil {
		return fmt.Errorf("open bucket: %v", err)
	}
	defer func() {
		if err := bucket.Close(); err != nil {
			log.Fatalf("close bucket: %v", err)
			os.Exit(1)
		}
	}()

	windows, err := getTimeRangeWindows(referenceTimeStr)
	if err != nil {
		return fmt.Errorf("get time range windows: %v", err)
	}

	for _, window := range windows {
		for _, metric := range metrics {
			err := exportMetricToBucketCSV(cmd.Context(), promAPI, bucket, k8sClusterID, metric, window)
			if err != nil {
				return fmt.Errorf("export metric %s to CSV: %v", metric, err)
			}
		}
	}

	return nil
}

// buildCSVHeaders builds the CSV header row from metric name and labels
func buildCSVHeaders(metricName string, matrix model.Matrix) []string {
	labelsDeduper := make(map[string]struct{})

	for _, series := range matrix {
		for label := range series.Metric {
			labelsDeduper[string(label)] = struct{}{}
		}
	}

	uniqueLabels := make([]string, 0, len(labelsDeduper))
	for label := range labelsDeduper {
		uniqueLabels = append(uniqueLabels, label)
	}
	sort.Strings(uniqueLabels)

	headers := make([]string, 0, 2+len(uniqueLabels))
	headers = append(headers, reservedColumnChargePeriodStart, metricName)
	headers = append(headers, uniqueLabels...)
	return headers
}

// matrixToCSV converts a Prometheus matrix to CSV format and writes it to the provided writer
func matrixToCSV(w *csv.Writer, matrix model.Matrix, metricName string) error {
	headers := buildCSVHeaders(metricName, matrix)
	indexForHeaderColumn := make(map[string]int, len(headers))
	for i, header := range headers {
		indexForHeaderColumn[header] = i
	}

	// Write header
	if err := w.Write(headers); err != nil {
		return fmt.Errorf("write CSV header: %w", err)
	}

	// Process each series in the matrix
	for _, series := range matrix {
		for _, point := range series.Values {
			ts := time.UnixMilli(int64(point.Timestamp)).UTC().Format(time.RFC3339)
			row := initializeCSVRowWithNULLMarkers(len(headers))
			row[indexForHeaderColumn[reservedColumnChargePeriodStart]] = ts
			row[indexForHeaderColumn[metricName]] = strconv.FormatFloat(float64(point.Value), 'f', -1, 64)

			for labelName, labelValue := range series.Metric {
				labelNameStr := string(labelName)

				index, ok := indexForHeaderColumn[labelNameStr]
				if !ok {
					return fmt.Errorf("label %s not found in headers", labelNameStr)
				} else if labelValue == "" {
					// treat empty label values as NULL
					continue
				} else {
					row[index] = string(labelValue)
				}
			}

			if err := w.Write(row); err != nil {
				return fmt.Errorf("write CSV row: %w", err)
			}
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush CSV writer: %w", err)
	}

	return nil
}

func exportMetricToBucketCSV(ctx context.Context, promAPI v1.API, bucket *blob.Bucket, k8sClusterID string, metricName string, window window) error {
	filename := fmt.Sprintf("%s_%s_%s.csv", k8sClusterID, metricName, window.Start.Format(time.DateOnly))
	query := metricName // QueryRange expects an instant vector, not a range vector

	result, warnings, err := promAPI.QueryRange(ctx, query, v1.Range{
		Start: window.Start,
		End:   window.End,
		Step:  1 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("query Prometheus: %w", err)
	}

	if len(warnings) > 0 {
		log.Printf("Warnings: %v\n", warnings)
	}

	matrix, ok := result.(model.Matrix)
	if !ok {
		return fmt.Errorf("unexpected result type: %T", result)
	}

	// Create a temporary file to store CSV data
	temp, err := os.CreateTemp("", "custom-metric-prometheus-*.csv")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		if err := os.Remove(temp.Name()); err != nil {
			log.Printf("remove temp file %s: %v", temp.Name(), err)
		}
	}()

	w := csv.NewWriter(temp)
	if err := matrixToCSV(w, matrix, metricName); err != nil {
		return fmt.Errorf("write matrix to CSV: %w", err)
	}

	// Sync and seek back to start of file
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if _, err := temp.Seek(0, 0); err != nil {
		return fmt.Errorf("seek temp file: %w", err)
	}

	// Write to bucket
	writer, err := bucket.NewWriter(ctx, filename, nil)
	if err != nil {
		return fmt.Errorf("initialize bucket writer: %w", err)
	}

	defer func() {
		if err := writer.Close(); err != nil {
			log.Fatalf("close bucket writer: %v", err)
		}
	}()

	if _, err := io.Copy(writer, temp); err != nil {
		return fmt.Errorf("copy to bucket: %w", err)
	}

	return nil
}

type window struct {
	Start time.Time
	End   time.Time
}

// getTimeRangeWindows will always return a slice of 2 windows:
// - The complete previous window
// - The current window
// TODO: Add support for multi-day long windows. Without this, if we miss a daily run we'll have to backfill right away
func getTimeRangeWindows(referenceTimeStr string) ([]window, error) {
	referenceTime := time.Now().UTC()
	return getTimeRangeWindowsWithTime(referenceTimeStr, referenceTime)
}

// getTimeRangeWindowsWithTime is the testable version that accepts a reference time
func getTimeRangeWindowsWithTime(referenceTimeStr string, now time.Time) ([]window, error) {
	referenceTime := now.UTC()

	if referenceTimeStr != "" {
		tryReferenceTime, err := time.Parse(time.DateOnly, referenceTimeStr)
		if err != nil {
			return nil, fmt.Errorf("parse reference time: %w", err)
		}
		referenceTime = tryReferenceTime.UTC()
	}

	startOfReferenceDate := time.Date(referenceTime.Year(), referenceTime.Month(), referenceTime.Day(), 0, 0, 0, 0, time.UTC)
	startOfPreviousDay := startOfReferenceDate.Add(-24 * time.Hour)
	startOfNextDay := startOfReferenceDate.Add(24 * time.Hour)

	windows := []window{
		{
			Start: startOfPreviousDay,
			End:   startOfReferenceDate,
		},
		{
			Start: startOfReferenceDate,
			End:   startOfNextDay,
		},
	}

	return windows, nil
}

func initializeCSVRowWithNULLMarkers(length int) []string {
	row := make([]string, length)
	for i := range row {
		row[i] = "NULL"
	}
	return row
}
