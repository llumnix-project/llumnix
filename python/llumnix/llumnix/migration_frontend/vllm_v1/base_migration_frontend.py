from abc import ABC, abstractmethod
from typing import Any, List, Tuple

from vllm.config import VllmConfig
from vllm.v1.request import Request

from llumnix import envs
from llumnix.instance_info import InstanceStatus
from llumnix.logging.logger import init_logger
from llumnix.utils import MigrationParams, MigrationType, RequestMigrationPolicy

logger = init_logger(__name__)

class BaseMigrationFrontend(ABC):
    """
    Abstract base class for handling request migration.
    It defines the common business logic for selecting migration candidates
    and leaves the I/O and state management implementation to subclasses.
    """

    def __init__(self, vllm_config: VllmConfig, dp_rank: int, scheduler: "Scheduler"):
        self._cfg = vllm_config
        self._dp_rank = dp_rank

        self.scheduler = scheduler
        self.get_detailed_migration_status = envs.LLUMNIX_DETAILED_MIG_STATUS
        self._is_shutdown = False


    @abstractmethod
    def shutdown(self):
        """Shuts down the frontend gracefully."""
        raise NotImplementedError

    @abstractmethod
    def _is_migrating(self, req: Request) -> bool:
        """Checks if a request is currently marked as migrating."""
        raise NotImplementedError

    @abstractmethod
    def _set_migrating_status(self, req: Request):
        """Marks a request as migrating."""
        raise NotImplementedError

    @abstractmethod
    def _cleanup_failed_migration(self, migrate_out_requests: List[Any]):
        """Clean failed migrating requests."""
        raise NotImplementedError

    @abstractmethod
    def update_migration_status(self, instance_status: InstanceStatus):
        """Updates the migration status."""
        raise NotImplementedError

    def clear_finished_reqs(self):
        pass

    def update_req_status(self, new_state: Any):
        pass

    def get_migrated_requests(self, migration_params: MigrationParams) -> List[Any]:
        if migration_params.migration_type == MigrationType.NUM_REQ and migration_params.num_reqs == -1:
            return self.iter_scheduler_requests()

        migrated_out_requests = []
        try:
            budget = self._get_budget(migration_params)
            running_reqs = self._get_running_requests()
            waiting_reqs = self._get_waiting_requests()

            if migration_params.mig_req_policy in (RequestMigrationPolicy.FCW, RequestMigrationPolicy.FCWSR):
                selected, remaining_budget = self._select_requests_from_list(
                    reqs=waiting_reqs,
                    indices=range(len(waiting_reqs)),
                    budget=budget,
                    migration_type=migration_params.migration_type,
                    is_in_waiting=True,
                )
                migrated_out_requests.extend(selected)

                if migration_params.mig_req_policy == RequestMigrationPolicy.FCWSR and remaining_budget > 0:
                    sorted_indices = self._get_sorted_indices(migration_params.mig_req_policy, running_reqs)
                    selected, _ = self._select_requests_from_list(
                        reqs=running_reqs,
                        indices=sorted_indices,
                        budget=remaining_budget,
                        migration_type=migration_params.migration_type,
                    )
                    migrated_out_requests.extend(selected)
            else:
                sorted_indices = self._get_sorted_indices(migration_params.mig_req_policy, running_reqs)
                selected, _ = self._select_requests_from_list(
                    reqs=running_reqs,
                    indices=sorted_indices,
                    budget=budget,
                    migration_type=migration_params.migration_type,
                )
                migrated_out_requests.extend(selected)
        # pylint: disable=broad-except
        except Exception:
            logger.exception("Get migration request failed")
            return []

        return migrated_out_requests

    def _select_requests_from_list(
        self, reqs: List[Any], indices: List[int], budget: int, migration_type: MigrationType, is_in_waiting: bool = False
    ) -> Tuple[List[Any], int]:
        selected_requests = []
        accumulated_cost = 0
        for i in indices:
            if accumulated_cost >= budget:
                break
            req = reqs[i]
            if self._should_skip_migration(req, is_in_waiting):
                continue
            req_cost = self._get_request_cost(req, migration_type)
            if req_cost > budget:
                continue
            selected_requests.append(req)
            self._set_migrating_status(req)
            accumulated_cost += req_cost

        return selected_requests, max(0, budget - accumulated_cost)

    def _get_sorted_indices(self, mig_policy: RequestMigrationPolicy, reqs: List[Any]) -> list[int]:
        indices = list(range(len(reqs)))
        if mig_policy == RequestMigrationPolicy.LCR.value:
            return list(reversed(indices))

        key_func = lambda i: self._get_sorting_key(reqs[i])
        if mig_policy in (RequestMigrationPolicy.SR.value, RequestMigrationPolicy.FCWSR.value):
            return sorted(indices, key=key_func)
        if mig_policy == RequestMigrationPolicy.LR.value:
            return sorted(indices, key=key_func, reverse=True)
        return indices

    @abstractmethod
    def _get_running_requests(self) -> List[Any]:
        raise NotImplementedError

    @abstractmethod
    def _get_waiting_requests(self) -> List[Any]:
        raise NotImplementedError

    @abstractmethod
    def iter_scheduler_requests(self) -> List[Any]:
        raise NotImplementedError

    @abstractmethod
    def _get_request_cost(self, req: Any, migration_type: MigrationType) -> int:
        raise NotImplementedError

    @abstractmethod
    def _get_sorting_key(self, req: Any) -> int:
        raise NotImplementedError

    @abstractmethod
    def _should_skip_migration(self, req: Any, is_in_waiting: bool) -> bool:
        raise NotImplementedError

    def _get_budget(self, migration_params: MigrationParams) -> int:
        if migration_params.migration_type == MigrationType.TOKEN:
            return migration_params.num_tokens
        if migration_params.migration_type == MigrationType.RATIO:
            total_tokens = self.scheduler.cache_config.num_gpu_blocks * self.scheduler.cache_config.block_size
            return int(total_tokens * migration_params.block_ratio)
        if migration_params.migration_type == MigrationType.NUM_REQ:
            return migration_params.num_reqs
        raise NotImplementedError("Not Implemented MigrationType")
