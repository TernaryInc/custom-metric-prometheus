# Custom Metric Prometheus

A command-line tool to fetch metrics from Prometheus and convert them to CSV format for use with Ternary.

## Installation

```bash
go install github.com/ternary/custom-metric-prometheus@latest
```

## Usage

```bash
custom-metric-prometheus \
  --prometheus-url="http://prometheus:9090" \
  --metrics="DCGM_FI_DEV_GPU_TEMP" \
  --metrics="DCGM_FI_DEV_GPU_UTIL" \
  --ternary-token="your-token" \
  --tenant-uuid="your-tenant-uuid"
```

### Required Flags

- `--prometheus-url`: Base URL of your Prometheus server
- `--metrics`: One or more metrics to fetch (can be specified multiple times)
- `--ternary-token`: Your Ternary API token
- `--tenant-uuid`: Your Tenant UUID

### Optional Flags

- `--ternary-url`: Base URL of Ternary Core API (defaults to https://core-api.ternary.app)

## Output

The tool outputs CSV data to stdout with the following format:
- First row contains headers: timestamp, all metric labels, and value
- Subsequent rows contain the corresponding values
- Timestamps are in Unix epoch format
- Each metric series is output separately with its own headers

## Example Output

```csv
timestamp,DCGM_FI_DRIVER_VERSION,Hostname,UUID,__name__,device,gpu,instance,job,kubernetes_node,modelName,pci_bus_id,value
1745982495,550.163.01,ip-192-168-102-213.ec2.internal,GPU-4a89731e-daf0-50b8-c9b4-ec2b28714713,DCGM_FI_DEV_GPU_TEMP,nvidia0,0,192.168.115.210:9400,gpu-metrics,ip-192-168-102-213.ec2.internal,NVIDIA A10G,00000000:00:1E.0,53
```
