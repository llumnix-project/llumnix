# Batch Inference

Llumnix LLM Gateway supports OpenAI's [Batch API](https://platform.openai.com/docs/guides/batch) (i.e., batch inference), following the OpenAI interface specification. Batch inference processes large volumes of inference requests asynchronously, making it ideal for running during idle periods to fully utilize machine resources.

The workflow is as follows:

1. Upload a file. Upload a JSONL file containing multiple inference requests.
2. Create a batch task. Create a batch task using the uploaded input file. LLM Gateway automatically shards the input file and processes multiple shards in parallel, sending inference requests to the inference service and recording the results.
3. Wait for the batch task to complete. You can check the task status through the query API.
4. Download the results. Once the task is completed, an output file and an error file (if any) are generated. You can retrieve the results through the file API.

## Architecture

```{mermaid}
graph LR
    Client[Client]

    subgraph Gateway["LLM Gateway"]
        FileAPI[File API]
        BatchAPI[Batch API]
        Reactor[Shard & Parallel Dispatch]
        InferenceAPI[Inference API]
    end

    OSS[(OSS)]
    Redis[(Redis)]
    Scheduler[Scheduler]
    Engine1[Inference Engine]
    Engine2[Inference Engine]

    Client -->|"1. Upload file"| FileAPI
    FileAPI -->|store| OSS
    Client -->|"2. Create batch"| BatchAPI
    BatchAPI -->|read/write metadata| Redis
    BatchAPI --> Reactor
    Reactor -->|"3. HTTP loopback<br/>(127.0.0.1)"| InferenceAPI
    InferenceAPI --> Scheduler
    Scheduler --> Engine1
    Scheduler --> Engine2
    Reactor -->|"4. Write results"| OSS
    Client -->|"5. Download results"| FileAPI
    FileAPI -->|read| OSS
```