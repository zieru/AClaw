import sqlite3
import sys

# Ensure UTF-8 output
sys.stdout.reconfigure(encoding='utf-8')

conn = sqlite3.connect('data/goassistant.db')
c = conn.cursor()
c.execute("SELECT id, timestamp, channel_type, user_name, provider, model, tools_called, status, client_request, provider_response, error_message, full_request_payload FROM audit_logs ORDER BY rowid DESC LIMIT 15")
rows = c.fetchall()
for r in rows:
    print("="*60)
    print(f"ID: {r[0]} | Time: {r[1]} | Ch: {r[2]} | User: {r[3]}")
    print(f"Prov: {r[4]} | Model: {r[5]} | Tools: {r[6]} | Status: {r[7]}")
    if r[10]:
        print(f"Err: {r[10]}")
    print(f"User Request:\n{r[8]}")
    print(f"Response:\n{r[9]}")
