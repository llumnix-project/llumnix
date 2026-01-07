build:
	@cargo build --release
	@cp target/release/libtokenizers_rs.a .
	@go build .

build-example:
	@docker build -f ./example/Dockerfile . -t tokenizers-example

release-darwin-%: test
	cargo build --release --target $*-apple-darwin
	mkdir -p artifacts/darwin-$*
	cp target/$*-apple-darwin/release/libtokenizers_rs.a artifacts/darwin-$*/libtokenizers_rs.a
	cd artifacts/darwin-$* && \
		tar -czf libtokenizers_rs.darwin-$*.tar.gz libtokenizers_rs.a
	mkdir -p artifacts/all
	cp artifacts/darwin-$*/libtokenizers_rs.darwin-$*.tar.gz artifacts/all/libtokenizers_rs.darwin-$*.tar.gz

release-linux-%: test
	docker buildx build --platform linux/$* --build-arg="DOCKER_TARGETPLATFORM=linux/$*" -f release/Dockerfile . -t tokenizers.linux-$*
	mkdir -p artifacts/linux-$*
	docker run -v $(PWD)/artifacts/linux-$*:/mnt --entrypoint ls tokenizers.linux-$* /workspace/tokenizers/lib/linux
	docker run -v $(PWD)/artifacts/linux-$*:/mnt --entrypoint cp tokenizers.linux-$* /workspace/tokenizers/lib/linux/$*/libtokenizers_rs.a /mnt/libtokenizers_rs.a
	cd artifacts/linux-$* && \
		tar -czf libtokenizers_rs.linux-$*.tar.gz libtokenizers_rs.a
	mkdir -p artifacts/all
	cp artifacts/linux-$*/libtokenizers_rs.linux-$*.tar.gz artifacts/all/libtokenizers_rs.linux-$*.tar.gz

release: release-darwin-aarch64 release-darwin-x86_64 release-linux-arm64 release-linux-x86_64
	cp artifacts/all/libtokenizers_rs.darwin-aarch64.tar.gz artifacts/all/libtokenizers_rs.darwin-arm64.tar.gz
	cp artifacts/all/libtokenizers_rs.linux-arm64.tar.gz artifacts/all/libtokenizers_rs.linux-aarch64.tar.gz
	cp artifacts/all/libtokenizers_rs.linux-x86_64.tar.gz artifacts/all/libtokenizers_rs.linux-amd64.tar.gz

test: build
	@go test -ldflags="-extldflags '-L./'" -v ./... -count=1

clean:
	rm -rf libtokenizers_rs.a target

bazel-sync:
	CARGO_BAZEL_REPIN=1 bazel sync --only=crate_index
