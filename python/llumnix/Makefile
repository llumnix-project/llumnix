CPU_CORES := $(shell nproc --all 2>/dev/null || echo 4)

.PHONY: vllm_install
vllm_install: proto
	@pip install -e .[vllm] --no-build-isolation

.PHONY: lint
lint: check_pylint_installed
	@err=0; \
	targets=$$(find llumnix -name "*.py" -not -path "*/\.*"); \
	echo "$$targets" | xargs pylint --rcfile=.pylintrc -s n --jobs=$(CPU_CORES) || err=1; \
	pylint --rcfile=.pylintrc -s n --jobs=$(CPU_CORES) setup.py || err=1; \
	if [ "$$err" -ne "0" ]; then exit 1; fi

##################################### pylint begin ######################################

PYLINT_VERSION = 2.12.2

.PHONY: check_pylint_installed
check_pylint_installed:
	@python3 -c "import pylint; assert pylint.__version__ == '$(PYLINT_VERSION)'" 2>/dev/null || { \
		echo "pylint is not installed or version does not match $(PYLINT_VERSION). Installing..."; \
		python3 -m pip install --force-reinstall pylint==$(PYLINT_VERSION); }

###################################### pylint end #######################################

###################################### proto begin ######################################
GRPCIO_TOOLS_MIN_VERSION = 1.59.5
GRPCIO_TOOLS_MAX_VERSION = 1.62.3

.PHONY: check_proto_installed
check_proto_installed:
	@python3 -c "import grpc_tools; from pkg_resources import parse_version; \
		version = pkg_resources.get_distribution('grpcio-tools').version; \
		assert parse_version('$(GRPCIO_TOOLS_MIN_VERSION)') <= parse_version(version) <= parse_version('$(GRPCIO_TOOLS_MAX_VERSION)')" 2>/dev/null || { \
		echo "grpcio-tools is not installed or version not in range $(GRPCIO_TOOLS_MIN_VERSION) to $(GRPCIO_TOOLS_MAX_VERSION). Installing..."; \
		python3 -m pip install "grpcio-tools>=$(GRPCIO_TOOLS_MIN_VERSION),<=$(GRPCIO_TOOLS_MAX_VERSION)"; }

.PHONY: proto
proto: check-grpcio-tools
	@find . -type d -name "proto" -not -path "./build/*" | while read dir; do \
	    dir_base=$$(dirname $$dir); \
	    find $$dir -name "*.proto" | while read proto_file; do \
	        echo "Compiling $$proto_file"; \
	        PYTHONWARNINGS="ignore::DeprecationWarning" python3 -m grpc_tools.protoc --proto_path=. --python_out=. --grpc_python_out=. --pyi_out=. $$proto_file; \
	    done; \
	done

.PHONY: check-grpcio-tools
check-grpcio-tools:
	@echo "Checking grpcio-tools..."
	@python3 -c "import grpc_tools" 2>/dev/null || \
	    (echo "grpcio-tools not found, installing..." && pip3 install grpcio-tools)

.PHONY: proto-clean
proto-clean:
	@find . -name "*_pb2_grpc.py" | xargs rm -f
	@find . -name "*_pb2.py" | xargs rm -f

####################################### proto end #######################################

##################################### pytest begin ######################################

.PHONY: check_pytest_installed
check_pytest_installed:
	@python3 -m pip show pytest > /dev/null 2>&1 || { \
		echo "pytest is not installed. Installing pytest ..."; \
		python3 -m pip install pytest; }

	@python3 -m pip show pytest-asyncio > /dev/null 2>&1 || { \
		echo "pytest-asyncio is not installed. Installing pytest-asyncio ..."; \
		python3 -m pip install pytest-asyncio; }

	@python3 -m pip show pytest-timeout > /dev/null 2>&1 || { \
		echo "pytest-timeout is not installed. Installing pytest-timeout ..."; \
		python3 -m pip install -i https://mirrors.aliyun.com/pypi/simple/ pytest-timeout; }

	@python3 -m pip show pytest-timer > /dev/null 2>&1 || { \
		echo "pytest-timer is not installed. Installing pytest-timer ..."; \
		python3 -m pip install -i https://mirrors.aliyun.com/pypi/simple/ pytest-timer; }

###################################### pytest end #######################################

###################################### test begin ######################################

.PHONY: cms_test
cms_test: check_pytest_installed
	@pytest -v --timer-top-n=999 --ignore=third_party --disable-warnings ./tests/cms/*

.PHONY: llumlet_test
llumlet_test: check_pytest_installed
	@pytest -v --timer-top-n=999 --ignore=third_party --disable-warnings ./tests/llumlet/*

####################################### test end #######################################
