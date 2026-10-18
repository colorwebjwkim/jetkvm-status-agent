"""Run the actual binary against a minimal local MQTT 3.1.1 fixture (no device)."""
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request


def packet(conn):
    def exact(n):
        result = b''
        while len(result) < n:
            part = conn.recv(n - len(result))
            if not part:
                raise EOFError()
            result += part
        return result
    kind = exact(1)[0]
    length, multiplier = 0, 1
    for _ in range(4):
        digit = exact(1)[0]
        length += (digit & 127) * multiplier
        if not digit & 128:
            return kind, exact(length)
        multiplier *= 128
    raise AssertionError('bad length')


def publish(conn, suffix, payload, retained=False):
    topic = ('jetkvm/test/' + suffix).encode()
    body = struct.pack('!H', len(topic)) + topic + json.dumps(payload).encode()
    assert len(body) < 128
    conn.sendall(bytes([0x31 if retained else 0x30, len(body)]) + body)


def main():
    binary = str(Path(sys.argv[1]).resolve())
    broker = socket.socket()
    broker.bind(('127.0.0.1', 0))
    broker.listen()
    broker.settimeout(15)
    probe = socket.socket()
    probe.bind(('127.0.0.1', 0))
    port = probe.getsockname()[1]
    probe.close()
    errors = []
    ready = threading.Event()
    reconnect = threading.Event()
    stop = threading.Event()

    def serve():
        try:
            for generation in range(2):
                conn, _ = broker.accept()
                with conn:
                    conn.settimeout(15)
                    assert packet(conn)[0] == 0x10
                    conn.sendall(b'\x20\x02\x00\x00')
                    kind, body = packet(conn)
                    assert kind == 0x82
                    conn.sendall(b'\x90\x06' + body[:2] + b'\x01' * 4)
                    publish(conn, 'status', {'online': True})
                    publish(conn, 'atx/state', {'power': True}, retained=True)
                    if generation == 0:
                        publish(conn, 'dc/state', {'isOn': False})
                        publish(conn, 'sessions/state', {'active_sessions': 2})
                        ready.set()
                        assert reconnect.wait(10)
                    else:
                        assert stop.wait(10)
        except Exception as exc:
            errors.append(repr(exc))

    config = Path('.local/smoke-config.json')
    config.parent.mkdir(exist_ok=True)
    config.write_text(json.dumps(dict(httpListen=f'127.0.0.1:{port}',
        mqttBroker=f'tcp://127.0.0.1:{broker.getsockname()[1]}',
        topicPrefix='jetkvm/test', staleAfterSeconds=1)))
    env = dict(os.environ, JETKVM_AGENT_TOKEN='smoke-only-token-' + 'x' * 32)
    thread = threading.Thread(target=serve, daemon=True)
    thread.start()
    process = subprocess.Popen([binary, '-config', str(config)], env=env)

    def get(path='/api/v1/status', auth=True):
        headers = {'Authorization': 'Bearer ' + env['JETKVM_AGENT_TOKEN']} if auth else {}
        with urllib.request.urlopen(urllib.request.Request(f'http://127.0.0.1:{port}' + path, headers=headers), timeout=2) as r:
            return json.load(r)

    def until(predicate):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            try:
                value = get()
                if predicate(value):
                    return value
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(.05)
        raise AssertionError('timed out')

    try:
        assert ready.wait(15)
        state = until(lambda s: s['sessions']['value'] == 2)
        assert state['power']['atx']['freshness'] == 'unknown'
        assert state['power']['dc']['value'] is False
        assert state['power']['dc']['freshness'] == 'fresh'
        assert state['device']['extension'] == 'unknown'
        assert state['power']['usableForControl'] is False
        assert get('/healthz', False) == {'ok': True}
        try:
            get(auth=False)
            raise AssertionError('auth bypass')
        except urllib.error.HTTPError as exc:
            assert exc.code == 401
        until(lambda s: s['power']['dc']['freshness'] == 'stale')
        reconnect.set()
        until(lambda s: s['agent']['collectorConnected'] and s['sessions']['value'] is None)
        assert not errors, errors
        print('PASS: MQTT -> authenticated HTTP, retained, TTL, unknown, reconnect reset, health/auth')
    finally:
        stop.set()
        process.terminate()
        process.wait(timeout=5)
        thread.join(timeout=5)
        broker.close()
    assert not errors, errors


if __name__ == '__main__':
    main()
