import paramiko
import os
import sys

sys.stdout.reconfigure(encoding='utf-8', errors='replace')

ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect('ca.tsel.my.id', port=22, username='zieru', password='zierong7', timeout=20)

sftp = ssh.open_sftp()
sftp.put("testweb-linux-amd64", "/home/zieru/testweb-linux-amd64")
sftp.close()

stdin, stdout, stderr = ssh.exec_command("chmod +x /home/zieru/testweb-linux-amd64 && /home/zieru/testweb-linux-amd64")
print("STDOUT:\n", stdout.read().decode('utf-8', errors='replace'))
print("STDERR:\n", stderr.read().decode('utf-8', errors='replace'))
ssh.close()
