package main

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/model"
)

func TestBuildOpenBucketURL(t *testing.T) {
	tests := []struct {
		name    string
		dest    string
		region  string
		prefix  string
		want    string
		wantErr bool
	}{
		{
			name:    "basic S3 URL without prefix",
			dest:    "s3://my-bucket",
			region:  "us-east-1",
			prefix:  "",
			want:    "s3://my-bucket?awssdk=v2&region=us-east-1",
			wantErr: false,
		},
		{
			name:    "S3 URL with prefix",
			dest:    "s3://my-bucket",
			region:  "us-west-2",
			prefix:  "metrics/",
			want:    "s3://my-bucket?awssdk=v2&prefix=metrics%2F&region=us-west-2",
			wantErr: false,
		},
		{
			name:    "prefix without trailing slash",
			dest:    "s3://my-bucket",
			region:  "us-east-1",
			prefix:  "metrics",
			want:    "s3://my-bucket?awssdk=v2&prefix=metrics%2F&region=us-east-1",
			wantErr: false,
		},
		{
			name:    "prefix with multiple trailing slashes",
			dest:    "s3://my-bucket",
			region:  "us-east-1",
			prefix:  "metrics///",
			want:    "s3://my-bucket?awssdk=v2&prefix=metrics%2F&region=us-east-1",
			wantErr: false,
		},
		{
			name:    "invalid URL",
			dest:    "://invalid",
			region:  "us-east-1",
			prefix:  "",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildOpenBucketURL(tt.dest, tt.region, tt.prefix)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildOpenBucketURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("buildOpenBucketURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetTimeRangeWindowsWithTime(t *testing.T) {
	// Use a fixed time for testing: 2024-01-15 12:00:00 UTC
	fixedTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		referenceTimeStr string
		now              time.Time
		wantWindows      []window
		wantErr          bool
	}{
		{
			name:             "with reference time string",
			referenceTimeStr: "2024-01-15",
			now:              fixedTime,
			wantWindows: []window{
				{
					Start: time.Date(2024, 1, 14, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
				},
			},
			wantErr: false,
		},
		{
			name:             "without reference time string",
			referenceTimeStr: "",
			now:              fixedTime,
			wantWindows: []window{
				{
					Start: time.Date(2024, 1, 14, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
				},
			},
			wantErr: false,
		},
		{
			name:             "invalid reference time format",
			referenceTimeStr: "invalid-date",
			now:              fixedTime,
			wantWindows:      nil,
			wantErr:          true,
		},
		{
			name:             "year boundary",
			referenceTimeStr: "2024-01-01",
			now:              fixedTime,
			wantWindows: []window{
				{
					Start: time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					End:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getTimeRangeWindowsWithTime(tt.referenceTimeStr, tt.now)
			if (err != nil) != tt.wantErr {
				t.Errorf("getTimeRangeWindowsWithTime() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(got) != len(tt.wantWindows) {
				t.Errorf("getTimeRangeWindowsWithTime() returned %d windows, want %d", len(got), len(tt.wantWindows))
				return
			}
			for i, w := range got {
				if !w.Start.Equal(tt.wantWindows[i].Start) || !w.End.Equal(tt.wantWindows[i].End) {
					t.Errorf("getTimeRangeWindowsWithTime() window[%d] = %v-%v, want %v-%v",
						i, w.Start, w.End, tt.wantWindows[i].Start, tt.wantWindows[i].End)
				}
			}
		})
	}
}

func TestExtractUniqueLabels(t *testing.T) {
	tests := []struct {
		name   string
		matrix model.Matrix
		want   []string
	}{
		{
			name: "single series with labels",
			matrix: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{
						"instance": "localhost:9090",
						"job":      "prometheus",
					},
				},
			},
			want: []string{"instance", "job"},
		},
		{
			name: "multiple series with overlapping labels",
			matrix: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{
						"instance": "localhost:9090",
						"job":      "prometheus",
					},
				},
				&model.SampleStream{
					Metric: model.Metric{
						"instance": "localhost:9091",
						"job":      "prometheus",
						"env":      "prod",
					},
				},
			},
			want: []string{"env", "instance", "job"},
		},
		{
			name: "excludes reserved column",
			matrix: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{
						"__name__": "some_metric",
						"instance": "localhost:9090",
					},
				},
			},
			want: []string{"instance"},
		},
		{
			name:   "empty matrix",
			matrix: model.Matrix{},
			want:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOrderedUniqueLabels(tt.matrix)
			if len(got) != len(tt.want) {
				t.Errorf("extractUniqueLabels() returned %d labels, want %d", len(got), len(tt.want))
				return
			}
			for i, label := range got {
				if label != tt.want[i] {
					t.Errorf("extractUniqueLabels()[%d] = %v, want %v", i, label, tt.want[i])
				}
			}
		})
	}
}

func TestBuildCSVHeaders(t *testing.T) {
	tests := []struct {
		name       string
		metricName string
		labels     []string
		want       []string
	}{
		{
			name:       "basic headers",
			metricName: "cpu_usage",
			labels:     []string{"instance", "job"},
			want:       []string{"ChargePeriodStart", "cpu_usage", "instance", "job"},
		},
		{
			name:       "no labels",
			metricName: "simple_metric",
			labels:     []string{},
			want:       []string{"ChargePeriodStart", "simple_metric"},
		},
		{
			name:       "many labels",
			metricName: "complex_metric",
			labels:     []string{"a", "b", "c", "d"},
			want:       []string{"ChargePeriodStart", "complex_metric", "a", "b", "c", "d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCSVHeaders([]string{tt.metricName}, tt.labels)
			if len(got) != len(tt.want) {
				t.Errorf("buildCSVHeaders() returned %d headers, want %d", len(got), len(tt.want))
				return
			}
			for i, header := range got {
				if header != tt.want[i] {
					t.Errorf("buildCSVHeaders()[%d] = %v, want %v", i, header, tt.want[i])
				}
			}
		})
	}
}

func TestMatrixToCSV(t *testing.T) {
	tests := []struct {
		name       string
		matrix     model.Matrix
		metricName string
		labels     []string
		wantRows   int
		wantErr    bool
	}{
		{
			name:       "single series single point",
			metricName: "test_metric",
			labels:     []string{"instance"},
			matrix: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{
						"instance": "localhost:9090",
					},
					Values: []model.SamplePair{
						{
							Timestamp: model.Time(1609459200000), // 2021-01-01 00:00:00 UTC
							Value:     42.5,
						},
					},
				},
			},
			wantRows: 2, // 1 header + 1 data row
			wantErr:  false,
		},
		{
			name:       "multiple series multiple points",
			metricName: "test_metric",
			labels:     []string{"instance"},
			matrix: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{
						"instance": "localhost:9090",
					},
					Values: []model.SamplePair{
						{
							Timestamp: model.Time(1609459200000),
							Value:     42.5,
						},
						{
							Timestamp: model.Time(1609462800000), // +1 hour
							Value:     43.0,
						},
					},
				},
				&model.SampleStream{
					Metric: model.Metric{
						"instance": "localhost:9091",
					},
					Values: []model.SamplePair{
						{
							Timestamp: model.Time(1609459200000),
							Value:     50.0,
						},
					},
				},
			},
			wantRows: 4, // 1 header + 3 data rows
			wantErr:  false,
		},
		{
			name:       "empty matrix",
			metricName: "test_metric",
			labels:     []string{},
			matrix:     model.Matrix{},
			wantRows:   1, // Just header
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			w := csv.NewWriter(&buf)

			err := matrixToCSV(w, tt.matrix, tt.metricName, []string{tt.metricName}, tt.labels)
			if (err != nil) != tt.wantErr {
				t.Errorf("matrixToCSV() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				reader := csv.NewReader(strings.NewReader(buf.String()))
				rows, err := reader.ReadAll()
				if err != nil {
					t.Errorf("Failed to parse CSV output: %v", err)
					return
				}
				if len(rows) != tt.wantRows {
					t.Errorf("matrixToCSV() produced %d rows, want %d", len(rows), tt.wantRows)
				}

				// Verify header structure
				if len(rows) > 0 {
					headers := rows[0]
					expectedHeaderLen := 2 + len(tt.labels) // ChargePeriodStart + metricName + labels
					if len(headers) != expectedHeaderLen {
						t.Errorf("Header length = %d, want %d", len(headers), expectedHeaderLen)
					}
					if headers[0] != "ChargePeriodStart" {
						t.Errorf("First header = %v, want ChargePeriodStart", headers[0])
					}
					if headers[1] != tt.metricName {
						t.Errorf("Second header = %v, want %v", headers[1], tt.metricName)
					}
				}
			}
		})
	}
}
