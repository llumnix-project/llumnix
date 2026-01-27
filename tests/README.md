# LLumnix Testing Framework

This directory contains the comprehensive testing suite for Llumnix, including base e2e tests and migration tests. Performance regression testing will be added in the future.

## Tests Structure

```bash
tests
|-- README.md       # This file
`-- local           # Local testing
```

## Development Prerequisites

Ensure your environment is ready for testing. The recommended Docker image is:
`beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev:llumnix-vllm-dev-20260123-111542`.

After setting up the Docker environment:

* Run `go mod tidy` to set up the Go environment for Gateway and Scheduler.
* Run `make lib-tokenizers-build` to build the tokenizers library before testing.

## Local Testing

Local testing runs test cases on your local machine. First of all, run `make vllm-install` to ensure the latest vllm patched llumlet is installed. For development convenience, you can install llumlet in development mode using `make llumlet-install`. Each test starts gateway, scheduler, redis, and vllm (colocated with llumlet), then sends requests to the gateway and verifies results. Run all tests with `make e2e-tests`.

**Simple Requests Test** (make simple-tests)

After all components are launched, requests are sent to the gateway to verify the results.

* Tests schedule policies, PD, and separate PD configurations.
* Covers stream/non-stream requests, max_tokens=1 and not 1.
* Tests both /v1/completions and /v1/chat/completions endpoints.

**Migration Test** (make migration-tests)

All requests are sent to one engine under the flood schedule policy; then Reschedule detects the imbalance and triggers migration.

* Tests load balance migration triggering without errors.
* Covers both PD and non-PD scenarios.
