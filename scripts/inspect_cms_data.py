from llumnix.cms.proto.cms_pb2 import InstanceMetadata, InstanceStatus
from llumnix.cms.cms_client import RedisClient, LLUMNIX_INSTANCE_METADATA_PREFIX, LLUMNIX_INSTANCE_STATUS_PREFIX

def inspect_all_cms_data():
    client = RedisClient()
    
    print("="*50)
    print("SCANNED CMS DATA")
    print("="*50)

    metadata_keys = client.get_keys_by_prefix(LLUMNIX_INSTANCE_METADATA_PREFIX)
    print(f"\n[Found {len(metadata_keys)} Metadata Records]")
    for key in metadata_keys:
        instance_id = key.replace(LLUMNIX_INSTANCE_METADATA_PREFIX, "")
        raw_data = client.get(key)
        if raw_data:
            metadata = InstanceMetadata()
            metadata.ParseFromString(raw_data)
            print(f"--- Instance ID: {instance_id} (Metadata) ---")
            print(metadata)

    status_keys = client.get_keys_by_prefix(LLUMNIX_INSTANCE_STATUS_PREFIX)
    print(f"\n[Found {len(status_keys)} Status Records]")
    for key in status_keys:
        instance_id = key.replace(LLUMNIX_INSTANCE_STATUS_PREFIX, "")
        raw_data = client.get(key)
        if raw_data:
            status = InstanceStatus()
            status.ParseFromString(raw_data)
            print(f"--- Instance ID: {instance_id} (Status) ---")
            print(status)

if __name__ == "__main__":
    # Make sure the environment variable is set, otherwise it will connect to the default address
    # e.g. export LLUMNIX_CMS_REDIS_ADDRESS=127.0.0.1, export LLUMNIX_CMS_REDIS_PORT=6379
    try:
        inspect_all_cms_data()
    except Exception as e:
        print(f"Error: {e}")
