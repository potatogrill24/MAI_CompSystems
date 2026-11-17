#!/usr/bin/env python3
"""End-to-end check against docker compose. Uses unique records and removes them."""
import concurrent.futures
import json
import time
import uuid
import urllib.error
import urllib.request

suffix = uuid.uuid4().hex[:10]

def call(port, method, path, body=None, status=200):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(f'http://127.0.0.1:{port}{path}', data=data,
                                 method=method, headers={'Content-Type': 'application/json'})
    try:
        response = urllib.request.urlopen(req, timeout=130)
    except urllib.error.HTTPError as e:
        response = e
    raw = response.read()
    assert response.code == status, (method, path, response.code, raw)
    return json.loads(raw) if raw else None

for port in range(8080, 8087):
    call(port, 'GET', '/readyz')

p = call(8081, 'POST', '/projects', {'order_name': 'Изготовить изделие', 'start_date': '01-01-2022'}, 201)
pid = p['id']
base = f'/projects/{pid}'
a = call(8081, 'POST', base + '/works', {'task': 'A', 'duration': 2, 'resource': 6}, 201)
b = call(8081, 'POST', base + '/works', {'task': 'B', 'duration': 3, 'resource': 4}, 201)
c = call(8081, 'POST', base + '/works', {'task': 'C', 'duration': 4, 'resource': 10}, 201)
call(8081, 'PUT', base + f'/works/{c["id"]}/predecessors', {'pred': [a['id'], b['id']]})
call(8081, 'PUT', base + f'/works/{a["id"]}/predecessors', {'pred': [c['id']]}, 400)
result = call(8081, 'POST', base + '/calculate', {'seed': 42})
assert result['duration'] == 7 and result['iterations'] == 1_000_000, result
call(8081, 'PATCH', base, {'order_name': 'Готово'})
call(8081, 'DELETE', base + f'/works/{a["id"]}', status=400)
call(8081, 'DELETE', base + f'/works/{c["id"]}', status=204)
call(8081, 'DELETE', base, status=204)
call(8081, 'GET', base, status=404)
print('OK management + million sequences + cycle validation')

code = 'product-' + suffix
order = 'order-' + suffix
product = {'code': code, 'name': 'Изделие', 'weight': 1.5, 'description': 'Описание'}
call(8083, 'POST', '/products', product, 201)
call(8084, 'POST', '/inventory', {'code': code, 'name': product['name'], 'quantity': 10, 'cost': '125.50'}, 201)
empty_code = code + '-empty'
call(8084, 'POST', '/inventory', {'code': empty_code, 'name': 'Empty', 'quantity': 0, 'cost': '1.00'}, 201)
call(8085, 'POST', '/orders', {'code': order + '-multi', 'date': '2026-11-17', 'items': [
    {'product_code': code, 'quantity': 1}, {'product_code': empty_code, 'quantity': 1}]}, 409)
assert call(8084, 'GET', '/inventory/' + code)['quantity'] == 10
call(8085, 'DELETE', '/orders/' + order + '-multi', status=204)
call(8084, 'DELETE', '/inventory/' + empty_code, status=204)
created = call(8085, 'POST', '/orders', {'code': order, 'date': '2026-11-17', 'items': [{'product_code': code, 'quantity': 3}]}, 201)
assert created['status'] == 'confirmed' and created['items'][0]['cost'] == '125.50', created
assert call(8084, 'GET', '/inventory/' + code)['quantity'] == 7
# Idempotent inventory retry must not subtract twice.
call(8084, 'POST', '/reservations', {'order_code': order, 'items': [{'product_code': code, 'quantity': 3}]})
assert call(8084, 'GET', '/inventory/' + code)['quantity'] == 7
q = call(8080, 'POST', '/graphql', {'query': 'query($code:String!){order(code:$code){code items{quantity product{code name weight}}} products{code}}', 'variables': {'code': order}})
assert 'errors' not in q and q['data']['order']['items'][0]['product']['code'] == code, q
call(8085, 'PATCH', '/orders/' + order, {'date': '2026-11-18'})
for _ in range(40):
    notifications = call(8086, 'GET', '/notifications')
    matching = [n for n in notifications if n['id'] == order + ':created']
    if matching:
        assert len(matching) == 1
        break
    time.sleep(0.5)
else:
    raise AssertionError('RabbitMQ notification not delivered')
print('OK products + order + inventory reserve + RabbitMQ + MongoDB notification + GraphQL')

# Concurrent orders: exactly one may consume the last seven units.
def order_seven(n):
    request = {'code': f'{order}-{n}', 'date': '2026-11-17', 'items': [{'product_code': code, 'quantity': 7}]}
    req = urllib.request.Request('http://127.0.0.1:8085/orders', data=json.dumps(request).encode(), method='POST', headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            return response.code
    except urllib.error.HTTPError as e:
        return e.code
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    statuses = list(pool.map(order_seven, range(2)))
assert sorted(statuses) == [201, 409], statuses
assert call(8084, 'GET', '/inventory/' + code)['quantity'] == 0
for n in range(2):
    call(8085, 'DELETE', f'/orders/{order}-{n}', status=204)
call(8085, 'DELETE', '/orders/' + order, status=204)
assert call(8084, 'GET', '/inventory/' + code)['quantity'] == 10
call(8085, 'GET', '/orders/' + order, status=404)
call(8084, 'DELETE', '/reservations/' + order, status=204)
assert call(8084, 'GET', '/inventory/' + code)['quantity'] == 10
call(8083, 'DELETE', '/products/' + code, status=204)
call(8084, 'DELETE', '/inventory/' + code, status=204)
print('OK concurrent stock protection + cancellation + stock release')
print('All smoke checks passed')
