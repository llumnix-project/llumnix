from typing import Any, Union, List

from llumnix.utils import RequestIDType


class LlumnixRequestOutput:
    def __init__(
        self,
        request_id: RequestIDType,
        instance_id: int,
        llumlet_grpc_address: str,
        engine_output: Any,
    ):
        self.request_id = request_id
        self.instance_id = instance_id
        self.llumlet_grpc_address = llumlet_grpc_address
        self.engine_output = engine_output

    def get_engine_output(self):
        return self.engine_output


class LlumnixRequestOutputs:
    """Wrapper of vLLM v1 EngineCoreOutputs"""

    def __init__(
        self,
        instance_id: int,
        llumlet_grpc_address: str,
        engine_outputs: Any,
    ):
        self.instance_id = instance_id
        self.llumlet_grpc_address = llumlet_grpc_address
        self.engine_outputs = engine_outputs

    def __iter__(self):
        for output in self.engine_outputs.outputs:
            yield LlumnixRequestOutput(
                output.request_id, self.instance_id, self.llumlet_grpc_address, output
            )


LlumnixRequestOutputsType = Union[List[LlumnixRequestOutput], LlumnixRequestOutputs]
