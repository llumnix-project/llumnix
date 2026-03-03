package processor

import (
	"llm-gateway/pkg/gateway/processor/registry"
	"llm-gateway/pkg/types"
	"time"

	"k8s.io/klog/v2"

	// Trigger init() registration of all pre/post processors.
	// DO NOT remove these imports — they activate processor registration via init().
	_ "llm-gateway/pkg/gateway/processor/post"
	_ "llm-gateway/pkg/gateway/processor/pre"
)

// Re-export registry types for external use.
// IMPORTANT: External packages MUST only import this package, NOT pre/post/registry directly.
type PreProcessor = registry.PreProcessor
type PostProcessor = registry.PostProcessor
type PreProcessorFactory = registry.PreProcessorFactory
type PostProcessorFactory = registry.PostProcessorFactory

// BuildPreProcessor creates a pre-processor instance by name with params.
func BuildPreProcessor(name string, params map[string]interface{}) PreProcessor {
	return registry.BuildPreProcessor(name, params)
}

// BuildPostProcessor creates a post-processor instance by name with params.
func BuildPostProcessor(name string, params map[string]interface{}) PostProcessor {
	return registry.BuildPostProcessor(name, params)
}

// RegisterPreProcessor registers a pre-processor factory with the given name.
func RegisterPreProcessor(name string, factory PreProcessorFactory) {
	registry.RegisterPreProcessor(name, factory)
}

// RegisterPostProcessor registers a post-processor factory with the given name.
func RegisterPostProcessor(name string, factory PostProcessorFactory) {
	registry.RegisterPostProcessor(name, factory)
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
	tStart := time.Now()
	defer func() {
		tCost := time.Since(tStart)
		req.RequestStats.PreprocessCost = tCost
	}()

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
func (post *PostProcessorChain) Process(req *types.RequestContext) error {
	tStart := time.Now()
	defer func() {
		tCost := time.Since(tStart)
		req.RequestStats.PostprocessCost += tCost
	}()

	for _, p := range post.postProcessors {
		if err := p.PostProcess(req); err != nil {
			klog.Errorf("[%s] post-processor %s execute failed: %v", req.Id, p.Name(), err)
			return err
		}
	}
	return nil
}

// ProcessStream runs all registered post-processors sequentially on the request stream.
func (post *PostProcessorChain) ProcessStream(req *types.RequestContext, done bool) error {
	tStart := time.Now()
	defer func() {
		tCost := time.Since(tStart)
		req.RequestStats.PostprocessCost += tCost
	}()

	for _, p := range post.postProcessors {
		if err := p.PostStreamProcess(req, done); err != nil {
			klog.Errorf("[%s] stream post-processor %s execute failed: %v", req.Id, p.Name(), err)
			return err
		}
	}
	return nil
}
