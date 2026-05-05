# Benchmark Results

Benchmark outputs should be written here.

Expected files:

- `*.json`: raw machine-readable results
- `*.csv`: tabular exports
- `report.md`: generated human-readable summary
- `charts/*.svg`: generated visual summaries linked from the report
- `comparison.csv` and `comparison.md`: optional baseline comparison outputs when `--compare-dir` is used

Generated benchmark outputs must not be committed. The repository `.gitignore` ignores everything under `benchmarks/results/` except this README.

To publish numbers, regenerate them locally from the documented commands and copy the relevant summary into release notes, documentation, or an external artifact store.
