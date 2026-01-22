.PHONY: proto
proto:
	@find . -type d -name "proto" -not -path "./build/*" | while read dir; do \
	    dir_base=$$(dirname $$dir); \
	    find $$dir -name "*.proto" | while read proto_file; do \
	        echo "Compiling $$proto_file"; \
	        PYTHONWARNINGS="ignore::DeprecationWarning" python3 -m grpc_tools.protoc --proto_path=. --python_out=. --grpc_python_out=. --pyi_out=. $$proto_file; \
	    done; \
	done;
