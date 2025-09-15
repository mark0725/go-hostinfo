# go-hostinfo
collect hostinfo: os,cpu,mem,disk,gpu to url.


## Usage

Pull dependencies and build
```bash
go mod tidy
go build -o go-hostinfo .
```

Run
```bash
./go-hostinfo -interval=1m -url=http://backend.example.com/collect
```

If you only want to debug and view the JSON output in the foreground:
```bash
./go-hostinfo -interval=10s -print
```

Example of Input/Output Fields
```json
{
"collected_at":"2025-09-15T15:10:00.123Z",
"os":{"platform":"ubuntu","family":"debian","version":"22.04","kernel":"6.5.0-1014"},
"cpu":{"model_name":"Intel(R) Xeon(R) Gold 6448Y","cores":32,"threads":64,"mhz":2300,"load_pct":11.52},
"memory":{"total_bytes":270008401920,"used_bytes":73728217088,"free_bytes":196280584192},
"disk":[{"mount":"/","fs_type":"ext4","total_bytes":1024209543168,"used_bytes":811056758784,"free_bytes":213152112384,"used_pct":79.18}],
"gpu":[
{
"index":0,"uuid":"GPU-69c951ff-c9cf-8eea-91c1-fd6ddccc0edb","name":"NVIDIA L40",
"temperature_c":39,"power_draw_w":37.43,"power_limit_w":300,"memory_total_mb":46068,
"memory_used_mb":17,"memory_free_mb":45452,"util_pct":0,
"driver_version":"570.124.06","cuda_version":"12.8"
},
...
],
"uptime_sec":123456,
"load_average":{"load1":0.42,"load5":0.38,"load15":0.35}
}
```

## License

MIT License
