package structs

// The common struct represent the llm inference instance
type LLMInstance struct {
	// the name of the llm instance, not same with machine instance
	// when unit_size > 1, the name usually is master node
	Name string
	// the infer mode of the llm instance
	InferMode string
	// the main entry point of the llm instance, usually is the dp_rank=0
	Ep Endpoint

	TPSize  int
	DPSize  int
	Workers []*LLMWorker
}

// When the dp_size or tp_size of the LLM instance is greater than 1,
// more detailed information about the workers needs to be collected.
type LLMWorker struct {
	// the worker id of each rank
	WorkerId string
	// the instance name
	InstName string
	// the infer mode of each rank, same as the llm instance
	InferMode string
	// the tp rank
	DPRank int
	// the dp rank
	TPRank int
	// the entry point of each rank
	Ep Endpoint
	// device id
	DeviceId int
	// pod name associated with this rank
	PodName string
}
