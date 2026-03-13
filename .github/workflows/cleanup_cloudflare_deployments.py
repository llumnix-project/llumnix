import os
import requests
from datetime import datetime

# Get configuration from environment variables
API_TOKEN = os.getenv('CLOUDFLARE_API_TOKEN')
ACCOUNT_ID = os.getenv('CLOUDFLARE_ACCOUNT_ID')
PROJECT_NAME = 'llumnix'

def check_environment():
    """Check if environment variables are correctly set"""
    if not API_TOKEN or not ACCOUNT_ID:
        print("Error: Missing required environment variables")
        print("Required: CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID")
        return False
    return True

def get_deployments(project_name):
    """Get all deployments for a specified project"""
    url = f"https://api.cloudflare.com/client/v4/accounts/{ACCOUNT_ID}/pages/projects/{project_name}/deployments"
    headers = {
        "Authorization": f"Bearer {API_TOKEN}",
        "Content-Type": "application/json"
    }
    
    response = requests.get(url, headers=headers)
    
    if response.status_code != 200:
        print(f"Failed to get deployments for project {project_name}: {response.text}")
        return None
        
    result = response.json()['result']
    return result

def delete_deployment(project_name, deployment_id):
    """Delete a specified deployment for a specified project"""
    url = f"https://api.cloudflare.com/client/v4/accounts/{ACCOUNT_ID}/pages/projects/{project_name}/deployments/{deployment_id}"
    headers = {
        "Authorization": f"Bearer {API_TOKEN}",
        "Content-Type": "application/json"
    }
    
    response = requests.delete(url, headers=headers)
    if response.status_code != 200:
        print(f"Failed to delete deployment {deployment_id}: {response.text}")
        return False
    return True

def main():
    if not check_environment():
        return

    print(f"Processing project: {PROJECT_NAME}")
    
    while True:
        # Get deployment records for this project
        deployments = get_deployments(PROJECT_NAME)
        if not deployments:
            print(f"No deployments found for project {PROJECT_NAME}")
            break

        print(f"Retrieved {len(deployments)} deployment records")
        
        # If deployment count is less than or equal to target, exit loop
        if len(deployments) <= 10:
            print(f"Project {PROJECT_NAME} has {len(deployments)} deployments, no cleanup needed")
            break
            
        # Sort by creation time
        sorted_deployments = sorted(deployments, key=lambda x: x['created_on'], reverse=True)
        
        # Keep the latest 10 deployments, delete the rest
        deployments_to_delete = sorted_deployments[10:]
        print(f"This round will delete {len(deployments_to_delete)} old deployments")
        
        for deployment in deployments_to_delete:
            deployment_id = deployment['id']
            created_date = datetime.fromisoformat(deployment['created_on'].replace('Z', '+00:00'))
            print(f"Deleting deployment {deployment_id} (created at {created_date})")
            if delete_deployment(PROJECT_NAME, deployment_id):
                print(f"Successfully deleted deployment {deployment_id}")
            else:
                print(f"Failed to delete deployment {deployment_id}")

if __name__ == "__main__":
    main()
