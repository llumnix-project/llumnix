from __future__ import annotations
import random
import time
from types import SimpleNamespace
from typing import Any, List, TYPE_CHECKING
from collections import defaultdict
from dataclasses import dataclass

import zmq
from zmq import Context
from sglang.srt.managers.schedule_batch import Req
from sglang.srt.managers.io_struct import (
    LlumletInstanceStatusReqOutput,
    MigrateOutReq,
    MigrateInReq,
    MigrateOutReqOutput,
    MigrateInReqOutput,
    TokenizedGenerateReqInput,
)
from sglang.srt.utils import get_zmq_socket

if TYPE_CHECKING:
    from sglang.srt.managers.scheduler import Scheduler

# pylint: disable-next=wrong-import-position
from llumnix.logging.logger import init_logger

# pylint: disable-next=wrong-import-position,cyclic-import
from llumnix.llumlet import Llumlet

# pylint: disable-next=wrong-import-position
from llumnix.utils import MigrationType

logger = init_logger(__name__)

step_id = 0


@dataclass
class SGLangConfig:
    enable_dp_attention: bool
    dp_rank: int
    moe_ep_rank: int
    attn_dp_rank: int
    instance_type: str
    dp_size: int
    dist_init_addr: str
    api_server_port: int


class SchedulerLlumnixMixin:
    def init_llumnix(
        self: Scheduler,
        context: Context,
    ):
        if self.pp_rank == 0 and self.attn_tp_rank == 0:
            engine_config = self.get_engine_config_for_llumlet()
            self.llumlet = Llumlet(
                engine_type="SGLang",
                engine_config=engine_config,
            )
            zmq_ipc_addresses = {}
            zmq_ipc_addresses = self.llumlet.add_llumlet_addresses(zmq_ipc_addresses)

            self.recv_from_llumlet = get_zmq_socket(
                context,
                zmq.PULL,
                zmq_ipc_addresses["llumlet_to_scheduler_ipc_name"],
                True,
            )
            self.send_to_llumlet = get_zmq_socket(
                context,
                zmq.PUSH,
                zmq_ipc_addresses["scheduler_to_llumlet_ipc_name"],
                True,
            )

            self.llumlet.start()
            self.detokenizer_zmq_socket_pool = {}
        else:
            self.recv_from_llumlet = None
            self.send_to_llumlet = SimpleNamespace(send_pyobj=lambda x: None)
            self.llumlet = None

    def get_instance_status_for_llumlet(
        self: Scheduler,
        recv_req: Any,  # pylint: disable=unused-argument
    ):
        ret = {}
        ret["num_running_requests"] = len(self.running_batch.reqs)
        ret["num_waiting_requests"] = len(self.waiting_queue)

        ret["num_total_gpu_tokens"] = self.max_total_num_tokens
        num_tokens_all_waiting_requests = 0
        for req in self.waiting_queue:
            tokens_needed = len(req.origin_input_ids) + len(req.output_ids)
            num_tokens_all_waiting_requests += tokens_needed
        ret["num_tokens_all_waiting_requests"] = num_tokens_all_waiting_requests

        if self.is_hybrid:
            full_num_used, swa_num_used, _, _, _, _, _, _ = self._get_swa_token_info()
            ret["num_used_gpu_tokens"] = max(full_num_used, swa_num_used)
        else:
            num_used, _, _, _ = self._get_token_info()
            ret["num_used_gpu_tokens"] = num_used

        if len(self.waiting_queue) > 0:
            first_req = self.waiting_queue[0]
            tokens_needed = len(first_req.origin_input_ids)
            ret["num_tokens_first_waiting_request"] = tokens_needed
        else:
            ret["num_tokens_first_waiting_request"] = 0

        ret["decode_batch_size"] = 0
        for req in self.running_batch.reqs:
            if len(req.output_ids) > 1:
                ret["decode_batch_size"] += 1

        global step_id
        ret["step_id"] = step_id
        step_id += 1

        return LlumletInstanceStatusReqOutput(instance_status=ret)

    def get_engine_config_for_llumlet(self: Scheduler):
        return SGLangConfig(
            enable_dp_attention=self.server_args.enable_dp_attention,
            dp_rank=self.dp_rank,
            moe_ep_rank=self.moe_ep_rank,
            attn_dp_rank=self.attn_dp_rank,
            instance_type=self.server_args.disaggregation_mode,
            dp_size=self.dp_size,
            dist_init_addr=self.server_args.dist_init_addr,
            api_server_port=self.server_args.port,
        )

    def get_external_detokenizer(
        self: Scheduler,
        reqs: List[Req],
    ):
        if not (self.pp_rank == 0 and self.attn_tp_rank == 0):
            return [], []
        external_detokenizers = []
        grouped_reqs: List[List[Req]] = []
        detokenizer_reqs_dict = defaultdict(list)
        for req in reqs:
            detokenizer_reqs_dict[req.src_detokenizer_ipc_name].append(req)

        for detokenizer_ipc_name, batch_reqs in detokenizer_reqs_dict.items():
            if detokenizer_ipc_name in self.detokenizer_zmq_socket_pool:
                socket = self.detokenizer_zmq_socket_pool[detokenizer_ipc_name]
            else:
                context = zmq.Context(2)
                socket = get_zmq_socket(context, zmq.PUSH, detokenizer_ipc_name, False)
                self.detokenizer_zmq_socket_pool[detokenizer_ipc_name] = socket

            external_detokenizers.append(socket)
            grouped_reqs.append(batch_reqs)

        return external_detokenizers, grouped_reqs

    def migrate_out(
        self: Scheduler,
        recv_req: MigrateOutReq,
    ) -> MigrateOutReqOutput:
        migrate_out_req_output = MigrateOutReqOutput(reqs=[])
        migration_type = recv_req.migration_params.migration_type
        selected_reqs_ids = []
        if migration_type == MigrationType.NUM_REQ:
            for idx, req in enumerate(self.running_batch.reqs):
                if req.disagg_kv_sender is not None:
                    continue
                selected_reqs_ids.append(idx)
                if len(selected_reqs_ids) >= recv_req.migration_params.num_reqs:
                    break
        elif migration_type == MigrationType.TOKEN:
            num_tokens = 0
            for idx, req in enumerate(self.running_batch.reqs):
                if req.disagg_kv_sender is not None:
                    continue
                selected_reqs_ids.append(idx)
                num_tokens += self.running_batch.reqs[idx].seqlen
                if num_tokens >= recv_req.migration_params.num_tokens:
                    break
        else:
            logger.error("Unsupported migration type %s", migration_type)

        for idx in selected_reqs_ids:
            migrate_out_req = self.running_batch.reqs[idx]
            migrate_out_req.bootstrap_room = random.randint(0, 2**63 - 1)
            if self.dp_size > 1:
                migrate_out_req.bootstrap_room = (
                    migrate_out_req.bootstrap_room // self.dp_size * self.dp_size
                    + self.dp_rank
                )
            migrate_out_req.bootstrap_port = (
                self.server_args.disaggregation_bootstrap_port
            )
            migrate_out_req.bootstrap_host = self.migration_bootstrap_host
            migrate_out_req.migrate_in_ip_address = recv_req.migrate_in_ip_address
            migrate_out_req.migrate_in_port = recv_req.migrate_in_port

            tokenized_req = TokenizedGenerateReqInput(
                migrate_out_req.rid,
                migrate_out_req.origin_input_text,
                migrate_out_req.origin_input_ids,
                None,
                migrate_out_req.sampling_params,
                migrate_out_req.return_logprob,
                None,
                migrate_out_req.top_logprobs_num,
                migrate_out_req.token_ids_logprob,
                migrate_out_req.stream,
                migrate_out_req.return_hidden_states,
                migrate_out_req.lora_id,
                migrate_out_req.input_embeds,
                custom_logit_processor=migrate_out_req.custom_logit_processor,
                bootstrap_host=migrate_out_req.bootstrap_host,
                bootstrap_port=migrate_out_req.bootstrap_port,
                bootstrap_room=migrate_out_req.bootstrap_room,
                data_parallel_rank=0,
            )
            migrate_out_req.seqlen_before_migration = migrate_out_req.seqlen
            detokenizer_ipc_name = (
                migrate_out_req.src_detokenizer_ipc_name
                if hasattr(migrate_out_req, "src_detokenizer_ipc_name")
                else self.detokenizer_ipc_name
            )
            migrate_in_req = MigrateInReq(
                tokenized_req,
                migrate_out_req.output_ids,
                migrate_out_req.fill_ids,
                migrate_out_req.seqlen_before_migration,
                detokenizer_ipc_name,
                migrate_out_req.send_token_offset,
                migrate_out_req.send_decode_id_offset,
            )
            migrate_out_req_output.reqs.append(migrate_in_req)
            migrate_out_req.migrate_start_time = time.time()
            self.migration_bootstrap_queue.add(
                migrate_out_req, self.model_config.num_key_value_heads
            )

        return migrate_out_req_output

    def migrate_in(
        self: Scheduler,
        recv_req: MigrateOutReqOutput,
    ):
        self.migrated_in = True
        for migrate_in_req in recv_req.reqs:
            tokenized_req = migrate_in_req.req
            req = Req(
                tokenized_req.rid,
                tokenized_req.input_text,
                tokenized_req.input_ids,
                tokenized_req.sampling_params,
                return_logprob=tokenized_req.return_logprob,
                top_logprobs_num=tokenized_req.top_logprobs_num,
                token_ids_logprob=tokenized_req.token_ids_logprob,
                stream=tokenized_req.stream,
                lora_id=tokenized_req.lora_id,
                input_embeds=tokenized_req.input_embeds,
                custom_logit_processor=tokenized_req.custom_logit_processor,
                return_hidden_states=tokenized_req.return_hidden_states,
                eos_token_ids=self.model_config.hf_eos_token_id,
                bootstrap_host=tokenized_req.bootstrap_host,
                bootstrap_port=tokenized_req.bootstrap_port,
                bootstrap_room=tokenized_req.bootstrap_room,
                data_parallel_rank=0,
                vocab_size=self.model_config.vocab_size,
            )
            req.tokenizer = self.tokenizer
            req.output_ids = migrate_in_req.output_ids
            req.fill_ids = migrate_in_req.fill_ids
            req.seqlen_before_migration = migrate_in_req.seqlen_before_migration
            req.src_detokenizer_ipc_name = migrate_in_req.detokenizer_ipc_name
            req.send_token_offset = migrate_in_req.send_token_offset
            req.send_decode_id_offset = migrate_in_req.send_decode_id_offset
            self.disagg_decode_prealloc_queue.add(req, is_migration=True)
        return MigrateInReqOutput(True, "")
