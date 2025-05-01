package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/spf13/cobra"
	"github.com/ternary/custom-metric-prometheus/pkg/ternary"
)

const rowLimit = 50_000 // unfixed backend error; can receive 413 entity too large error. Send 50,000 rows for now.

var (
	prometheusURL string
	metrics       []string
	ternaryURL    string
	ternaryToken  string
	tenantUUID    string
	debug         bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "custom-metric-prometheus",
		Short: "A tool to fetch Prometheus metrics and convert them to CSV format",
		RunE:  run,
	}

	rootCmd.Flags().StringVar(&prometheusURL, "prometheus-url", "", "Base URL of Prometheus server (required)")
	rootCmd.Flags().StringSliceVar(&metrics, "metrics", []string{}, "List of metrics to fetch (required)")
	rootCmd.Flags().StringVar(&ternaryURL, "ternary-url", "https://core-api.ternary.app", "Base URL of Ternary Core API")
	rootCmd.Flags().StringVar(&ternaryToken, "ternary-token", os.Getenv("TERNARY_TOKEN"), "Ternary API token (required, can be set via TERNARY_TOKEN env var)")
	rootCmd.Flags().StringVar(&tenantUUID, "tenant-uuid", "", "Tenant UUID (required)")
	rootCmd.Flags().BoolVar(&debug, "debug", false, "Enable debug mode for API client")

	rootCmd.MarkFlagRequired("prometheus-url")
	rootCmd.MarkFlagRequired("metrics")
	rootCmd.MarkFlagRequired("tenant-uuid")

	// Only mark token as required if not set via environment variable
	if ternaryToken == "" {
		rootCmd.MarkFlagRequired("ternary-token")
	}

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

	// Initialize Ternary client
	ternaryClient := ternary.NewClient(ternaryURL, ternaryToken)
	ternaryClient.SetDebug(debug)

	v1api := v1.NewAPI(promClient)

	// Process each metric
	for i, metric := range metrics {
		if i > 0 {
			time.Sleep(3 * time.Second) // unfixed race condition posting many metrics at once
		}

		if err := processMetric(v1api, ternaryClient, metric); err != nil {
			return fmt.Errorf("error processing metric %s: %v", metric, err)
		}
	}

	return nil
}

func processMetric(v1api v1.API, ternaryClient *ternary.Client, metric string) error {
	ctx := context.Background()
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

	var rowCount = 0
	w := csv.NewWriter(temp)

	// Create schema
	schema := make(ternary.Schema)
	var labels []string

	// First pass to get metadata
	for _, series := range matrix {
		// Add all labels as dimensions
		for label := range series.Metric {
			// Protected BigQuery column name
			if label != "__name__" {
				schema[string(label)] = ternary.FieldTypeDimension
			}
		}
	}

	for label := range schema {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	// Don't include in `labels`
	schema["timestamp"] = ternary.FieldTypeTimestamp
	schema["value"] = ternary.FieldTypeMeasure

	// Write header
	header := append([]string{"timestamp", "value"}, labels...)
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

			rowCount++

			if rowCount >= rowLimit {
				break
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

	// Encode CSV data as base64
	var buf bytes.Buffer
	csvEncoder := base64.NewEncoder(base64.StdEncoding, &buf)
	if _, err := io.Copy(csvEncoder, temp); err != nil {
		return fmt.Errorf("error encoding CSV data: %v", err)
	}
	if err := csvEncoder.Close(); err != nil {
		return fmt.Errorf("error closing base64 encoder: %v", err)
	}
	csvData := buf.String()

	// Create or update metric in Ternary
	description := fmt.Sprintf("Prometheus metric %s from %s", metric, prometheusURL)
	_, err = ternaryClient.FindOrCreateMetric(metric, description, tenantUUID, csvData, schema)
	if err != nil {
		return fmt.Errorf("error creating/updating metric in Ternary: %v", err)
	}

	return nil
}
