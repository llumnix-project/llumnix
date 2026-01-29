# CRC Checksum Logic

### Data Structures
1. **`data`**: `std::vector<std::vector<IpcBlock>>`
 - Outer vector: Indexed by tensor (one vector per tensor).
 - Inner vector: A list of all IpcBlocks for that tensor.
 - Each IpcBlock contains: src_offset, dst_offset, and length.

2. **`layer_gdrcpy_mem_`**: `std::vector<std::unique_ptr<GdrMemDesc>>`
   - Stored as a flattened list：`[layer0_tensor0, layer0_tensor1, layer1_tensor0, layer1_tensor1, ...]`
   - Indexing method：`layer_idx * num_tensors_per_layer + tensor_idx`

### Prefill node CRC Calculation Flow（send_data）
1. `send_data(layer_idx)` is called for each layer.
2. Iterate through the IpcBlocks of each tensor.
3. Get the CPU pointer using `get_layer_cpu_ptr(ctx, layer_idx)`.
4. For each IpcBlock, calculate the CRC using its `src_offset`:`crc32_z(crc, layer_cpu_ptr + src_offset, len)`
5. Accumulate the CRCs from all layers into `self.crc_`

### Prefill Sending the CRC Request（get_remote_crc）
1. Calculate the total number of IpcBlocks, `bodycnt`
2. Send a request containing:`lcrc`（the locally computed CRC）+ `bodycnt` + all `(dst_offset, length)` pairs from the IpcBlocks. The IpcBlocks sent are those obtained from the `parse_block` call on the last layer. In the current model, all layers have identical block information. This may need to be adapted for future models where layers have different block information.

### Decode node CRC Calculation（resp_remote_crc）
1. Receive `local_crc` and `offlencnt`
2. Receive all `(offset, length)` pairs
3. Iterate through all `layer_descs`. For each layer_desc：
   - Calculate the CRC using all the received offset/length pairs.
