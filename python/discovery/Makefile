.PHONY: check-grpcio-tools
check-grpcio-tools:
	@echo "Checking grpcio-tools..."
	@python3 -c "import grpc_tools" 2>/dev/null || \
	    (echo "grpcio-tools not found, installing..." && pip3 install grpcio-tools)

.PHONY: proto
proto: check-grpcio-tools
	@find . -type d -name "proto" -not -path "./build/*" | while read dir; do \
	    dir_base=$$(dirname $$dir); \
	    find $$dir -name "*.proto" | while read proto_file; do \
	        echo "Compiling $$proto_file"; \
	        PYTHONWARNINGS="ignore::DeprecationWarning" python3 -m grpc_tools.protoc --proto_path=. --python_out=. --grpc_python_out=. --pyi_out=. $$proto_file; \
	    done; \
	done;

.PHONY: install
install:
	@pip install -e . --no-build-isolation

.PHONY: test
test:
	@pip install pytest pytest-asyncio --quiet
	@pytest tests/ -v
