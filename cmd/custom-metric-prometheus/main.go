package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
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
	prometheusURL    string
	cohort           string
	metrics          []string
	destination      string
	prefix           string
	referenceTimeStr string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "custom-metric-prometheus",
		Short: "A tool to fetch Prometheus metrics and convert them to CSV format",
		RunE:  run,
	}

	rootCmd.Flags().StringVar(&prometheusURL, "prometheus-url", "", "Base URL of Prometheus server (required)")
	rootCmd.Flags().StringVar(&cohort, "cohort", "untitled", "Cohort or identifier to separate metrics deliveries from other origins")
	rootCmd.Flags().StringSliceVar(&metrics, "metrics", []string{}, "List of metrics to fetch (required)")
	rootCmd.Flags().StringVar(&destination, "dest", "", "Destination URL e.g. s3://bucket")
	rootCmd.Flags().StringVar(&prefix, "prefix", "", "Destination prefix to write within bucket (optional)")
	rootCmd.Flags().StringVar(&referenceTimeStr, "reference-time", "", "Metrics export as of this time (optional)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	// Initialize Prometheus client
	promClient, err := api.NewClient(api.Config{
		Address: prometheusURL,
	})
	if err != nil {
		return fmt.Errorf("error creating Prometheus client: %v", err)
	}

	v1api := v1.NewAPI(promClient)

	// we will always add a slash to the prefix so we don't have to check for it later
	prefix = strings.TrimSuffix(prefix, "/")

	bucket, err := blob.OpenBucket(context.Background(), destination)
	if err != nil {
		return fmt.Errorf("error opening bucket: %v", err)
	}

	var referenceTime time.Time
	if referenceTimeStr != "" {
		referenceTime, err = time.Parse(time.DateOnly, referenceTimeStr)
		if err != nil {
			return fmt.Errorf("error parsing reference time: %v", err)
		}
	} else {
		referenceTime = time.Now()
	}

	windows, err := getTimeRangeWindows(referenceTime)
	if err != nil {
		return fmt.Errorf("error getting time range windows: %v", err)
	}

	for _, window := range windows {
		for _, metric := range metrics {
			if err := processMetric(v1api, bucket, prefix, metric, window); err != nil {
				return fmt.Errorf("error processing metric %s: %v", metric, err)
			}
		}
	}

	return nil
}

func processMetric(v1api v1.API, bucket *blob.Bucket, prefix, metric string, window [2]time.Time) error {
	ctx := context.Background()
	filenameForWindow := fmt.Sprintf("%s/%s_%s_%s.csv", prefix, metric, cohort, window[0].Format(time.DateOnly))
	timeRange := fmt.Sprintf("%s[1d]", metric)
	result, warnings, err := v1api.Query(ctx, timeRange, time.Now())
	if err != nil {
		return fmt.Errorf("error querying Prometheus: %v", err)
	}

	if len(warnings) > 0 {
		fmt.Fprintf(os.Stderr, "Warnings: %v\n", warnings)
	}

	matrix, ok := result.(model.Matrix)
	if !ok {
		return fmt.Errorf("unexpected result type: %T", result)
	}

	// Create a buffer to store CSV data
	temp, err := os.CreateTemp("", "custom-metric-prometheus-*.csv")
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}

	w := csv.NewWriter(temp)

	// First pass to get metadata
	var labels []string
	for _, series := range matrix {
		// Add all labels as dimensions
		for label := range series.Metric {
			// Protected BigQuery column name
			if label != "__name__" {
				labels = append(labels, string(label))
			}
		}
	}
	sort.Strings(labels)

	// Write header
	header := append([]string{"ChargePeriodStart", "MetricValue"}, labels...)
	if err := w.Write(header); err != nil {
		return fmt.Errorf("error writing CSV header: %v", err)
	}

	// Process each series in the matrix
	for _, series := range matrix {
		// Write data rows
		for _, point := range series.Values {
			// Format timestamp in RFC3339
			ts := time.UnixMilli(int64(point.Timestamp)).UTC().Format(time.RFC3339)

			row := make([]string, 0, len(labels)+2)
			row = append(row, ts)
			row = append(row, strconv.FormatFloat(float64(point.Value), 'f', -1, 64))

			// Add label values in the same order as headers
			for _, label := range labels {
				row = append(row, string(series.Metric[model.LabelName(label)]))
			}

			if len(header) != len(row) {
				return fmt.Errorf("header and row length mismatch: %d != %d", len(header), len(row))
			}

			if err := w.Write(row); err != nil {
				return fmt.Errorf("error writing CSV row: %v", err)
			}
		}
	}

	// Ensure all CSV data is written to the temp file
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("error flushing CSV writer: %v", err)
	}

	// Make sure to sync the file to disk
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("error syncing temp file: %v", err)
	}

	// Seek back to start of file
	if _, err := temp.Seek(0, 0); err != nil {
		return fmt.Errorf("error seeking temp file: %v", err)
	}

	writer, err := bucket.NewWriter(ctx, filenameForWindow, nil)
	if err != nil {
		return fmt.Errorf("error writing to bucket: %v", err)
	}

	if _, err := io.Copy(writer, temp); err != nil {
		return fmt.Errorf("error copying to bucket: %v", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("error closing bucket writer: %v", err)
	}

	return nil
}

// getTimeRangeWindows will always return a slice of 2 windows:
// - The complete previous window
// - The current window
// TODO: Add support for multi-day long windows. Without this, if we miss a daily run we'll have to backfill right away
func getTimeRangeWindows(now time.Time) ([][2]time.Time, error) {
	midnightToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	midnightYesterday := midnightToday.Add(-24 * time.Hour)
	windows := [][2]time.Time{
		{midnightYesterday, midnightToday},
		{midnightToday, now},
	}
	return windows, nil
}
