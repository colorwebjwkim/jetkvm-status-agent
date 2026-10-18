"""Temporary publish-only compatibility receiver; never changes monitor settings.

Uses the configured JetKVM source IP, acknowledges QoS 1, rejects subscriptions.
The PC port must be free. Report contains no endpoint, device ID or credentials.
"""
import argparse
import json
from pathlib import Path
import socket
import struct
import time
from smoke import packet

p = argparse.ArgumentParser()
p.add_argument('--bind', required=True)
p.add_argument('--device-ip', required=True)
p.add_argument('--port', type=int, default=1883)
p.add_argument('--seconds', type=int, default=45)
p.add_argument('--report', required=True)
args = p.parse_args()
report = dict(connected=False, sessions=None, online=None, sessionMessages=0,
              commandsSent=0, subscriptions=0, retainedMessages=0, result='INCOMPLETE')
deadline = time.monotonic() + args.seconds
with socket.socket() as server:
    server.bind((args.bind, args.port))
    server.listen(2)
    server.settimeout(1)
    print('Publish-only PC compatibility receiver ready.', flush=True)
    while time.monotonic() < deadline:
        try:
            conn, peer = server.accept()
        except socket.timeout:
            continue
        with conn:
            if peer[0] != args.device_ip:
                continue
            conn.settimeout(10)
            try:
                kind, body = packet(conn)
                if kind != 0x10:
                    continue
                conn.sendall(b'\x20\x02\x00\x00')
                report['connected'] = True
                while time.monotonic() < deadline:
                    kind, body = packet(conn)
                    if kind >> 4 == 3:
                        size = struct.unpack('!H', body[:2])[0]
                        topic = body[2:2+size].decode()
                        index = 2+size
                        qos = (kind >> 1) & 3
                        if qos == 1:
                            message_id = body[index:index+2]
                            index += 2
                        value = json.loads(body[index:])
                        if topic.endswith('/sessions/state'):
                            count = value.get('active_sessions')
                            if type(count) is not int or count < 0:
                                raise ValueError('Invalid sessions')
                            report['sessions'] = count
                            report['sessionMessages'] += 1
                        elif topic.endswith('/status'):
                            report['online'] = value.get('online')
                        else:
                            raise ValueError('Unexpected relay topic')
                        report['retainedMessages'] += bool(kind & 1)
                        if qos == 1:
                            conn.sendall(b'\x40\x02' + message_id)
                    elif kind == 0xC0:
                        conn.sendall(b'\xD0\x00')
                    elif kind >> 4 == 8:
                        report['subscriptions'] += 1
                        break
                    elif kind == 0xE0:
                        break
            except (EOFError, OSError):
                pass
if report['sessionMessages'] >= 2 and not report['subscriptions'] and not report['retainedMessages']:
    report['result'] = 'PASS_DEVICE_TO_PC_RELAY'
Path(args.report).parent.mkdir(parents=True, exist_ok=True)
Path(args.report).write_text(json.dumps(report, indent=2), encoding='utf-8')
print(json.dumps(report))
if report['result'] != 'PASS_DEVICE_TO_PC_RELAY':
    raise SystemExit(1)
