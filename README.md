# Custom Metric Prometheus

A command-line tool to fetch metrics from Prometheus and upload them to Ternary as custom metrics.

## Installation

```bash
go install github.com/ternary/custom-metric-prometheus@latest
```

## Usage

```bash
# Using command line flags
custom-metric-prometheus \
  --prometheus-url="http://prometheus:9090" \
  --metrics="DCGM_FI_DEV_GPU_TEMP" \
  --metrics="DCGM_FI_DEV_GPU_UTIL" \
  --ternary-token="your-token" \
  --tenant-uuid="your-tenant-uuid"

# Using environment variable for token
export TERNARY_TOKEN="your-token"
custom-metric-prometheus \
  --prometheus-url="http://prometheus:9090" \
  --metrics="DCGM_FI_DEV_GPU_TEMP" \
  --tenant-uuid="your-tenant-uuid"
```

### Required Flags

- `--prometheus-url`: Base URL of your Prometheus server
- `--metrics`: One or more metrics to fetch (can be specified multiple times)
- `--ternary-token`: Your Ternary API token (can be set via TERNARY_TOKEN environment variable)
- `--tenant-uuid`: Your Tenant UUID

### Optional Flags

- `--ternary-url`: Base URL of Ternary Core API (defaults to https://core-api.ternary.app)

### Environment Variables

- `TERNARY_TOKEN`: Can be used instead of the `--ternary-token` flag

## Operation

The tool performs the following steps for each specified metric:

1. Queries the Prometheus API for the metric's values over the past 24 hours
2. Converts the data to CSV format, including:
   - Timestamp (Unix epoch)
   - All metric labels (e.g., instance, job, etc.)
   - Metric value
3. Base64 encodes the CSV data
4. Checks if a custom metric with the same name exists in Ternary
   - If it exists: Updates the existing metric with new data
   - If not: Creates a new custom metric

## Example Data Format

Before base64 encoding, the CSV data looks like this:

```csv
timestamp,DCGM_FI_DRIVER_VERSION,Hostname,UUID,__name__,device,gpu,instance,job,kubernetes_node,modelName,pci_bus_id,value
1745982495,550.163.01,ip-192-168-102-213.ec2.internal,GPU-4a89731e-daf0-50b8-c9b4-ec2b28714713,DCGM_FI_DEV_GPU_TEMP,nvidia0,0,192.168.115.210:9400,gpu-metrics,ip-192-168-102-213.ec2.internal,NVIDIA A10G,00000000:00:1E.0,53
```

## Error Handling

The tool includes comprehensive error handling for:
- Prometheus API connection and query issues
- CSV data formatting
- Ternary API authentication and data submission
- Network connectivity problems

All errors are reported with descriptive messages to help diagnose issues.
