package structs

// RequestProcessor defines the interface for request/response processors
type RequestProcessor interface {
	IsApplicable(req *Request) bool
	Preprocess(req *Request) error
	PostProcess(req *Request, data []byte) []byte
	PostProcessStream(req *Request, data []byte, isFirstChunk bool) ([]byte, error)
	BufferStreamToResp(req *Request, data []byte, isFirstChunk bool) ([]byte, error)
	Type() string
}
