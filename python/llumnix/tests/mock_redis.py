# Copyright (c) 2024, Alibaba Group;
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at

# http://www.apache.org/licenses/LICENSE-2.0

# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from llumnix.cms.cms_client import RedisClient
from llumnix.cms.cms_client_async import RedisClient as AsyncRedisClient


class MockRedisClient(RedisClient):
    def __init__(self):
        self.data = {}

    def set(self, key, value):
        self.data[key] = value

    def get(self, key):
        return self.data.get(key, None)

    def remove(self, key):
        if key in self.data:
            del self.data[key]

    def get_keys_by_prefix(self, prefix: str):
        return [key for key in self.data if key.startswith(prefix)]

    def mget(self, keys: str):
        res = []
        for key in keys:
            res.append(self.data.get(key, None))
        return res


class MockAsyncRedisClient(AsyncRedisClient):
    def __init__(self):
        self.data = {}

    async def set(self, key, value, ex=None):
        self.data[key] = value

    async def get(self, key):
        return self.data.get(key, None)

    async def remove(self, key):
        if key in self.data:
            del self.data[key]

    async def get_keys_by_prefix(self, prefix: str):
        return [key for key in self.data if key.startswith(prefix)]

    async def mget(self, keys: str):
        res = []
        for key in keys:
            res.append(self.data.get(key, None))
        return res
