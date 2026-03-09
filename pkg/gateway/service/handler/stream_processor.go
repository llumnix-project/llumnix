package handler

import (
	"io"
	"llm-gateway/pkg/gateway/service/backend"
	"llm-gateway/pkg/types"

	"k8s.io/klog/v2"
)

// ChunkParser defines the protocol-specific parsing strategy for response chunks.
// Each handler implementation provides its own parsing logic based on the underlying
// protocol format (e.g., OpenAI SSE format, Anthropic format).
type ChunkParser interface {
	// ParseChunk deserializes raw chunk bytes into the internal response structure
	// stored in RequestContext. Returns io.EOF if the chunk indicates stream end.
	ParseChunk(reqCtx *types.RequestContext, data []byte) error
}

// ChunkWriter defines the protocol-specific post-processing and writing strategy.
// This interface encapsulates the final stage of chunk handling: post-processing,
// serialization, and writing to the response channel.
type ChunkWriter interface {
	// ProcessAndWriteChunk executes post-processing pipeline and writes the chunk
	// to the client. The 'done' parameter indicates whether this is the final chunk.
	ProcessAndWriteChunk(reqCtx *types.RequestContext, done bool) error
}

// StreamProcessor implements the generic streaming inference mechanism.
// It separates the invariant control flow (mechanism) from the protocol-specific
// processing logic (policy) through the Strategy Pattern.
//
// Architecture: Mechanism-Policy Separation
// - Mechanism: chunk iteration, error handling, timing control, resource cleanup
// - Policy: chunk parsing and writing (injected via ChunkParser and ChunkWriter)
type StreamProcessor struct {
	parser ChunkParser
	writer ChunkWriter
}

// NewStreamProcessor creates a streaming processor with injected strategies.
// The parser and writer encapsulate protocol-specific behavior, while the processor
// provides the reusable streaming mechanism.
func NewStreamProcessor(parser ChunkParser, writer ChunkWriter) *StreamProcessor {
	return &StreamProcessor{
		parser: parser,
		writer: writer,
	}
}

// ProcessStream executes the generic streaming inference loop with protocol-specific strategies.
// This method encapsulates the invariant streaming mechanism:
// 1. Chunk iteration and EOF detection
// 2. Prefill/decode timing hooks
// 3. Error handling (unexpected errors vs normal EOF)
// 4. Max tokens truncation
// 5. Resource cleanup via deferred function
// 6. Context cancellation detection to prevent goroutine leak
//
// The actual parsing and writing logic is delegated to the injected strategy implementations.
func (sp *StreamProcessor) ProcessStream(req *types.RequestContext, chunkChan <-chan backend.StreamChunk) {

	processFunc := func(isFirst bool, chunk backend.StreamChunk) bool {
		klog.V(3).Infof("received stream chunk: %s, err: %v", string(chunk.Data), chunk.Err)

		defer func() {
			// Track timing metrics: first chunk triggers prefill completion, subsequent chunks trigger decode
			if isFirst {
				req.TriggerPostPrefillStream()
			} else {
				req.TriggerPostDecodeStreamChunk()
			}
		}()

		// Handle unexpected streaming errors (early return for exception path)
		if chunk.Err != nil && chunk.Err != io.EOF {
			klog.Errorf("request %s error during stream inference: %v", req.Id, chunk.Err)
			req.WriteErrorResponse(chunk.Err)
			return true
		}

		// Handle normal stream end - process any remaining data before completing
		if chunk.Err == io.EOF {
			if len(chunk.Data) == 0 {
				return true
			}
			// Parse and process the final chunk that came with EOF
			err := sp.parser.ParseChunk(req, chunk.Data)
			if err != nil && err != io.EOF {
				klog.Errorf("failed to parse final response: %v", err)
				req.WriteErrorResponse(err)
				return true
			}
			if err := sp.writer.ProcessAndWriteChunk(req, true); err != nil {
				klog.Errorf("failed to process final chunk: %v", err)
				req.WriteErrorResponse(err)
				return true
			}
			return true
		}

		// Parse the chunk data into internal response structure
		err := sp.parser.ParseChunk(req, chunk.Data)
		if err != nil && err != io.EOF {
			klog.Errorf("failed to parse response: %v", err)
			req.WriteErrorResponse(err)
			return true
		}

		// Process through post-processor chain and write to client
		if err := sp.writer.ProcessAndWriteChunk(req, (err == io.EOF)); err != nil {
			klog.Errorf("failed to process and write chunk: %v", err)
			req.WriteErrorResponse(err)
			return true
		}

		// Check if maximum token limit has been reached
		if req.OutputExceedMaxTokens() {
			// Send final chunk to gracefully end the stream
			if err := sp.writer.ProcessAndWriteChunk(req, true); err != nil {
				klog.Errorf("failed to write final chunk: %v", err)
			}
			return true
		}

		return false
	}

	isFirst := true
	for {
		select {
		case chunk, ok := <-chunkChan:
			if !ok {
				return // Channel closed, stream ended
			}
			if processFunc(isFirst, chunk) {
				return
			}
			isFirst = false
		case <-req.Context.Done():
			klog.V(3).Infof("request %s context cancelled, stopping stream processing", req.Id)
			return
		}
	}
}
