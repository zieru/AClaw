import paramiko
import os
import sys

sys.stdout.reconfigure(encoding='utf-8', errors='replace')

local_bin = "goassistant-linux-amd64"
remote_temp = "/home/zieru/AClaw/goassistant-linux-amd64.tmp"
remote_target = "/home/zieru/AClaw/goassistant-linux-amd64"

print(f"Connecting to ca.tsel.my.id...")
ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect('ca.tsel.my.id', port=22, username='zieru', password='zierong7', timeout=30)

sftp = ssh.open_sftp()
file_size = os.path.getsize(local_bin)
print(f"Uploading {local_bin} ({file_size / (1024*1024):.1f} MB) -> {remote_temp}...")

def progress(transferred, total):
    percent = (transferred / total) * 100
    if transferred % (10 * 1024 * 1024) < 65536 or transferred == total:
        print(f"Progress: {transferred / (1024*1024):.1f} MB / {total / (1024*1024):.1f} MB ({percent:.1f}%)")

sftp.put(local_bin, remote_temp, callback=progress)
sftp.close()
print("Upload done. Replacing binary and setting executable...")

# Replace binary
stdin, stdout, stderr = ssh.exec_command(f"mv {remote_temp} {remote_target} && chmod +x {remote_target}")
stdout.channel.recv_exit_status()

print("Restarting aclaw service via doas...")
import time
cmd = "doas rc-service aclaw restart"
stdin, stdout, stderr = ssh.exec_command(cmd, get_pty=True)
time.sleep(0.5)
try:
    stdin.write("zierong7\n")
    stdin.flush()
except Exception:
    pass

out = stdout.read().decode('utf-8', errors='replace')
print("Restart output:", out)

time.sleep(2)
stdin, stdout, stderr = ssh.exec_command("ps aux | grep goassist | grep -v grep")
print("Running processes:\n", stdout.read().decode('utf-8', errors='replace'))

ssh.close()
print("Deployment completed successfully!")
