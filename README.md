# Custom Metric Prometheus

EXPERIMENTAL. Use at your own risk.

A command-line tool to fetch metrics from Prometheus, convert them to CSV format, and deposit them in blob storage (e.g., S3) for use with Ternary BYOD (Bring Your Own Data).

## Installation

```bash
go install github.com/ternary/custom-metric-prometheus@latest
```

## Usage

```bash
# Using command line flags
custom-metric-prometheus \
  --prometheus-url="http://prometheus:9090" \
  --dest="s3://bucket-name" \
  --k8s-cluster-id="gpu-demo" \
  --prefix="test-metrics-0/" \
  --metrics="DCGM_FI_DEV_GPU_TEMP" \
  --metrics="DCGM_FI_DEV_GPU_UTIL" \
  --labels="instance" \
  --labels="job" \
  --labels="device" \
  --labels="gpu"
```

### Required Flags

- `--prometheus-url`: Base URL of your Prometheus server
- `--dest`: Destination URL for blob storage (e.g., `s3://bucket-name`)
- `--metrics`: One or more metrics to fetch (can be specified multiple times)
- `--k8s-cluster-id`: Unique Kubernetes cluster ID to disambiguate output files
- `--prefix`: Path prefix to write within bucket (e.g., `test-metrics-0/`)

### Optional Flags

- `--labels`: List of labels to include in CSV schema (can be specified multiple times). If not provided or empty, a warning will be logged and the CSV will not include any label columns.
- `--aws-bucket-region`: AWS region for S3 bucket (default: `us-east-1`)
- `--reference-time`: Metrics export as of this date in YYYY-MM-DD format; current UTC+0 date by default

## Operation

The tool performs the following steps for each specified metric:

1. Queries the Prometheus API for the metric's values over two time windows:
   - The complete previous day (24-hour window)
   - The current day (24-hour window from reference time)
   - Uses 1-hour step intervals for data points
2. Converts the data to CSV format with the following structure:
   - First column: `ChargePeriodStart` (RFC3339 format timestamp, e.g., "2024-03-20T15:04:05Z")
   - Next columns: All metric names (one column per metric)
   - Remaining columns: Labels declared via the `--labels` flag (e.g., instance, job, device, gpu, etc.)
   - Note: The CSV schema is determined by the `--labels` flag. Labels not declared via `--labels` will not appear in the CSV output, even if they exist in the Prometheus data. If `--labels` is empty, a warning is logged and no label columns are included.
3. Writes CSV files directly to blob storage (S3) with filenames in the format:
   `{k8s-cluster-id}_{metric-name}_{date}.csv`

## Example Data Format

```tsv
ChargePeriodStart	DCGM_FI_DEV_GPU_TEMP	Hostname	UUID	container	device	endpoint	gpu	instance	job	modelName	namespace	pci_bus_id	pod	service
2025-12-10T00:00:00Z	22	ip-192-168-100-41.ec2.internal	GPU-be2af801-3af1-d8a0-98eb-ed03b2bb8673	exporter	nvidia0	metrics	0	192.168.113.133:9400	dcgm-exporter	Tesla T4	gpu-metrics	00000000:00:1E.0	dcgm-exporter-zvddj	dcgm-exporter
```

The CSV format uses:
- `ChargePeriodStart` as the timestamp column (reserved column name for Ternary BYOD)
- The metric name(s) as column(s) containing the metric values
- Labels declared via the `--labels` flag as additional columns. Only labels explicitly declared via `--labels` will appear in the CSV output.

## Error Handling

The tool includes comprehensive error handling for:
- Prometheus API connection and query issues
- CSV data formatting
- Blob storage connectivity and write operations
- Network connectivity problems

All errors are reported with descriptive messages to help diagnose issues.
