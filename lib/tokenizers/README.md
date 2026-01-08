# Tokenizers

Go bindings for the [HuggingFace Tokenizers](https://github.com/huggingface/tokenizers) library.

## Installation

`make build` to build `libtokenizers_rs.a` that you need to run your application that uses bindings. In addition, you need to inform the linker where to find that static library: `go run -ldflags="-extldflags '-L./path/to/libtokenizers/directory'" .` or just add it to the `CGO_LDFLAGS` environment variable: `CGO_LDFLAGS="-L./path/to/libtokenizers/directory"` to avoid specifying it every time.

## Getting started

TLDR: [working example](example/main.go).

## Benchmarks

### Tiktoken vs HuggingFace

Tiktoken is 3x faster on most tasks.

```
> go test . -ldflags="-extldflags '-L.'" -run=^\$ -bench=. -benchmem -count=1 -benchtime=1s

goos: darwin
goarch: arm64
pkg: github.com/daulet/tokenizers
cpu: Apple M1 Pro
BenchmarkEncodeNTimes/huggingface-10              133966             10456 ns/op             256 B/op         12 allocs/op
BenchmarkEncodeNTimes/tiktoken-10                 339538              3759 ns/op              88 B/op          4 allocs/op
BenchmarkEncodeNChars/huggingface-10            456006800                2.798 ns/op           0 B/op          0 allocs/op
BenchmarkEncodeNChars/tiktoken-10               615315394                2.959 ns/op           0 B/op          0 allocs/op
BenchmarkDecodeNTimes/huggingface-10              817164              1489 ns/op              64 B/op          2 allocs/op
BenchmarkDecodeNTimes/tiktoken-10                2369224               513.9 ns/op            64 B/op          2 allocs/op
BenchmarkDecodeNTokens/huggingface-10            7423770               170.8 ns/op             4 B/op          0 allocs/op
BenchmarkDecodeNTokens/tiktoken-10              80597544                19.40 ns/op            4 B/op          0 allocs/op
PASS
ok      github.com/daulet/tokenizers    40.626s
```

### Go vs Rust

`go test . -run=^\$ -bench=. -benchmem -count=10 > test/benchmark/$(git rev-parse HEAD).txt`

Decoding overhead (due to CGO and extra allocations) is between 2% to 9% depending on the benchmark.

```bash
go test . -bench=. -benchmem -benchtime=10s

goos: darwin
goarch: arm64
pkg: github.com/daulet/tokenizers
BenchmarkEncodeNTimes-10     	  959494	     12622 ns/op	     232 B/op	      12 allocs/op
BenchmarkEncodeNChars-10      1000000000	     2.046 ns/op	       0 B/op	       0 allocs/op
BenchmarkDecodeNTimes-10     	 2758072	      4345 ns/op	      96 B/op	       3 allocs/op
BenchmarkDecodeNTokens-10    	18689725	     648.5 ns/op	       7 B/op	       0 allocs/op
PASS
ok   github.com/daulet/tokenizers
```

Run equivalent Rust tests with `cargo bench`.

```bash
decode_n_times          time:   [3.9812 µs 3.9874 µs 3.9939 µs]
                        change: [-0.4103% -0.1338% +0.1275%] (p = 0.33 > 0.05)
                        No change in performance detected.
Found 7 outliers among 100 measurements (7.00%)
  7 (7.00%) high mild

decode_n_tokens         time:   [651.72 ns 661.73 ns 675.78 ns]
                        change: [+0.3504% +2.0016% +3.5507%] (p = 0.01 < 0.05)
                        Change within noise threshold.
Found 7 outliers among 100 measurements (7.00%)
  2 (2.00%) high mild
  5 (5.00%) high severe
```

## Contributing

Please refer to [CONTRIBUTING.md](CONTRIBUTING.md) for information on how to contribute a PR to this project.
