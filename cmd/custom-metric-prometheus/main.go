package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"fmt"
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

var (
	prometheusURL string
	metrics       []string
	ternaryURL    string
	ternaryToken  string
	tenantUUID    string
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

	v1api := v1.NewAPI(promClient)

	// Process each metric
	for _, metric := range metrics {
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

	// Process each series in the matrix
	for _, series := range matrix {
		// Get all metric labels
		labels := make([]string, 0, len(series.Metric))
		for name := range series.Metric {
			labels = append(labels, string(name))
		}
		sort.Strings(labels)

		// Create schema
		schema := ternary.Schema{
			"timestamp": ternary.FieldTypeTimestamp,
			"value":     ternary.FieldTypeMeasure,
		}
		// Add all labels as dimensions
		for _, label := range labels {
			schema[label] = ternary.FieldTypeDimension
		}

		// Create a buffer to store CSV data
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)

		// Write header
		header := append([]string{"timestamp"}, labels...)
		header = append(header, "value")
		if err := w.Write(header); err != nil {
			return fmt.Errorf("error writing CSV header: %v", err)
		}

		// Write data rows
		for _, point := range series.Values {
			row := make([]string, 0, len(labels)+2)
			// Format timestamp in RFC3339
			ts := time.Unix(int64(point.Timestamp), 0).UTC().Format(time.RFC3339)
			row = append(row, ts)

			// Add label values in the same order as headers
			for _, label := range labels {
				row = append(row, string(series.Metric[model.LabelName(label)]))
			}

			row = append(row, strconv.FormatFloat(float64(point.Value), 'f', -1, 64))
			if err := w.Write(row); err != nil {
				return fmt.Errorf("error writing CSV row: %v", err)
			}
		}

		w.Flush()
		if err := w.Error(); err != nil {
			return fmt.Errorf("error flushing CSV writer: %v", err)
		}

		// Encode CSV data as base64
		csvData := base64.StdEncoding.EncodeToString(buf.Bytes())

		// Create or update metric in Ternary
		description := fmt.Sprintf("Prometheus metric %s from %s", metric, prometheusURL)
		_, err := ternaryClient.FindOrCreateMetric(metric, description, tenantUUID, csvData, schema)
		if err != nil {
			return fmt.Errorf("error creating/updating metric in Ternary: %v", err)
		}
	}

	return nil
}
