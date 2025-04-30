package main

import (
	"context"
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
	rootCmd.Flags().StringVar(&ternaryToken, "ternary-token", "", "Ternary API token (required)")
	rootCmd.Flags().StringVar(&tenantUUID, "tenant-uuid", "", "Tenant UUID (required)")

	rootCmd.MarkFlagRequired("prometheus-url")
	rootCmd.MarkFlagRequired("metrics")
	rootCmd.MarkFlagRequired("ternary-token")
	rootCmd.MarkFlagRequired("tenant-uuid")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	client, err := api.NewClient(api.Config{
		Address: prometheusURL,
	})
	if err != nil {
		return fmt.Errorf("error creating client: %v", err)
	}

	v1api := v1.NewAPI(client)

	// Create CSV writer
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()

	// Process each metric
	for _, metric := range metrics {
		if err := processMetric(v1api, w, metric); err != nil {
			return fmt.Errorf("error processing metric %s: %v", metric, err)
		}
	}

	return nil
}

func processMetric(v1api v1.API, w *csv.Writer, metric string) error {
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

		// Write header if this is the first series
		header := append([]string{"timestamp"}, labels...)
		header = append(header, "value")
		if err := w.Write(header); err != nil {
			return fmt.Errorf("error writing CSV header: %v", err)
		}

		// Write data rows
		for _, point := range series.Values {
			row := make([]string, 0, len(labels)+2)
			row = append(row, fmt.Sprintf("%d", point.Timestamp.Unix()))

			// Add label values in the same order as headers
			for _, label := range labels {
				row = append(row, string(series.Metric[model.LabelName(label)]))
			}

			row = append(row, strconv.FormatFloat(float64(point.Value), 'f', -1, 64))
			if err := w.Write(row); err != nil {
				return fmt.Errorf("error writing CSV row: %v", err)
			}
		}
	}

	return nil
}
