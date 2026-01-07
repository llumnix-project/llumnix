from enum import Enum


class RequestInferenceType(str, Enum):
    UNKNOWN = "unknown"
    PREFILL = "prefill"
    DECODE = "decode"
    PREFILL_AND_DECODE = "prefill_and_decode"

    @classmethod
    def generate_inference_type(cls, exist_prefill: bool, exist_decode: bool):
        if exist_prefill and exist_decode:
            inference_type = RequestInferenceType.PREFILL_AND_DECODE
        elif exist_prefill:
            inference_type = RequestInferenceType.PREFILL
        elif exist_decode:
            inference_type = RequestInferenceType.DECODE
        else:
            inference_type = RequestInferenceType.UNKNOWN

        return RequestInferenceType(inference_type)
