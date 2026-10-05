import paramiko

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect('ca.tsel.my.id', port=22, username='zieru', password='zierong7', timeout=15)
sftp = ssh.open_sftp()

files = [
    ("scripts/browser_use_mcp/Dockerfile", "/home/zieru/AClaw/scripts/browser_use_mcp/Dockerfile"),
    ("scripts/browser_use_mcp/server.py", "/home/zieru/AClaw/scripts/browser_use_mcp/server.py"),
    ("scripts/browser_use_mcp/pyproject.toml", "/home/zieru/AClaw/scripts/browser_use_mcp/pyproject.toml"),
    ("scripts/browser_use_mcp/README.md", "/home/zieru/AClaw/scripts/browser_use_mcp/README.md"),
    ("configs/default_config.yaml", "/home/zieru/AClaw/configs/default_config.yaml")
]

for local_path, remote_path in files:
    print(f"Uploading {local_path} -> {remote_path}...")
    sftp.put(local_path, remote_path)

sftp.close()
ssh.close()
print("Upload complete!")
