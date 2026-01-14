package processor

import (
	"easgo/pkg/llm-gateway/types"

	"k8s.io/klog/v2"
)

// ProcessResult represents the result of a processor execution.
// It can be one of: ProcessStop, ProcessContinue, or ProcessRetry.
type ProcessResult int

const (
	// ProcessStop indicates that processing should stop immediately.
	ProcessStop ProcessResult = iota
	// ProcessContinue indicates that processing should continue to the next processor.
	ProcessContinue
	// ProcessRetry indicates that the request should be retried.
	ProcessRetry
)

// String returns a human-readable string representation of the ProcessResult.
func (r ProcessResult) String() string {
	switch r {
	case ProcessStop:
		return "ProcessStop"
	case ProcessContinue:
		return "ProcessContinue"
	case ProcessRetry:
		return "ProcessRetry"
	default:
		return "Unknown"
	}
}

// PreProcessor defines the interface for pre-processing requests.
// Pre-processors are executed before proxy processing logic.
type PreProcessor interface {
	// Name returns the name of the pre-processor.
	Name() string
	// PreProcess processes the request and returns a error.
	PreProcess(request *types.RequestContext) error
}

// PostProcessor defines the interface for post-processing requests.
// Post-processors are executed after the main processing logic.
type PostProcessor interface {
	// Name returns the name of the post-processor.
	Name() string
	// PostProcess processes the request and returns a error.
	PostProcess(request *types.RequestContext, done bool) error
}

// PreProcessorChain implements a chain of pre-processors.
// It executes pre-processors in the order they were registered.
type PreProcessorChain struct {
	preProcessors []PreProcessor
}

// CreatePreProcessorChain creates and returns a new PreProcessorChain.
func CreatePreProcessorChain() *PreProcessorChain {
	return &PreProcessorChain{}
}

// Register adds a pre-processor to the chain.
func (pc *PreProcessorChain) Register(p PreProcessor) {
	pc.preProcessors = append(pc.preProcessors, p)
}

// Process runs all registered pre-processors sequentially on the request.
// If any pre-processor returns an error, execution stops and that result is returned.
func (pc *PreProcessorChain) Process(req *types.RequestContext) error {
	for _, p := range pc.preProcessors {
		if err := p.PreProcess(req); err != nil {
			klog.Errorf("[%s] pre-processor %s execute failed: %v", req.Id, p.Name(), err)
			return err
		}
	}
	return nil
}

// PostProcessorChain implements a chain of post-processors.
// It executes post-processors in the order they were registered.
type PostProcessorChain struct {
	postProcessors []PostProcessor
}

// CreatePostProcessorChain creates and returns a new PostProcessorChain.
func CreatePostProcessorChain() *PostProcessorChain {
	return &PostProcessorChain{}
}

// Register adds a post-processor to the chain.
func (post *PostProcessorChain) Register(p PostProcessor) {
	post.postProcessors = append(post.postProcessors, p)
}

// Process runs all registered post-processors sequentially on the request.
// If any post-processor returns a result other than ProcessContinue,
// execution stops and that result is returned.
func (post *PostProcessorChain) Process(req *types.RequestContext, done bool) error {
	for _, p := range post.postProcessors {
		if err := p.PostProcess(req, done); err != nil {
			klog.Errorf("[%s] post-processor %s execute failed: %v", req.Id, p.Name(), err)
			return err
		}
	}
	return nil
}
