#!/usr/bin/env python3
"""Check retry after RabbitMQ and Inventory outages in this project's Compose stack."""
import json
from pathlib import Path
import subprocess
import time
import urllib.request
import urllib.error
import uuid

root = Path(__file__).resolve().parents[1]

def compose(*args):
    subprocess.run(['docker', 'compose', *args], cwd=root, check=True,
                   stdout=subprocess.DEVNULL)

def call(port, method, path, body=None, status=200):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(f'http://127.0.0.1:{port}{path}', data=data, method=method,
                                 headers={'Content-Type': 'application/json'})
    try:
        response = urllib.request.urlopen(req, timeout=130)
    except urllib.error.HTTPError as e:
        response = e
    raw = response.read()
    assert response.code == status, (response.code, raw)
    return json.loads(raw) if raw else None

def wait_until(fn, seconds=90):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            if fn():
                return
        except (OSError, AssertionError):
            pass
        time.sleep(1)
    raise AssertionError('recovery timed out')

code = 'recovery-' + uuid.uuid4().hex[:10]
one, two = code + '-1', code + '-2'
call(8084, 'POST', '/inventory', {'code': code, 'name': 'Recovery test', 'quantity': 4, 'cost': '1.00'}, 201)
try:
    compose('stop', 'rabbitmq')
    order = call(8085, 'POST', '/orders', {'code': one, 'date': '2026-11-17', 'items': [{'product_code': code, 'quantity': 1}]}, 201)
    assert order['status'] == 'confirmed'
    compose('start', 'rabbitmq')
    wait_until(lambda: any(n['id'] == one + ':created' for n in call(8086, 'GET', '/notifications')))
    print('OK confirmed order and deferred notification survive RabbitMQ outage')
    compose('stop', 'inventory')
    order = call(8085, 'POST', '/orders', {'code': two, 'date': '2026-11-17', 'items': [{'product_code': code, 'quantity': 1}]}, 202)
    assert order['status'] == 'pending'
    compose('start', 'inventory')
    wait_until(lambda: call(8085, 'GET', '/orders/' + two)['status'] == 'confirmed')
    assert call(8084, 'GET', '/inventory/' + code)['quantity'] == 2
    print('OK pending order is retried after Inventory recovers')
finally:
    compose('start', 'rabbitmq', 'inventory')
    wait_until(lambda: call(8084, 'GET', '/readyz')['status'] == 'ready')
    for order_code in [one, two]:
        try:
            call(8085, 'DELETE', '/orders/' + order_code, status=204)
        except AssertionError:
            pass
    call(8084, 'DELETE', '/inventory/' + code, status=204)
