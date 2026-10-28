#!/usr/bin/env python3
"""Read-only business observer for the owned Web gate, never consumes SSE.

Authentication uses the gate's synthetic account and keeps its cookie only
in memory. No configuration secret is requested and no business POST,
plugin invocation, control-file request or application file write is performed.
Only --out writes an evidence file. The current held receiver has no read API;
its producer receipts are labeled, never replaced with invented counter values.
"""
import argparse
import datetime
import http.cookiejar
import json
import os
import pathlib
import sys
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state', type=pathlib.Path, required=True)
    parser.add_argument('--phase', required=True)
    parser.add_argument('--previous', type=pathlib.Path)
    parser.add_argument('--out', type=pathlib.Path)
    parser.add_argument('--recipient-receipts', type=pathlib.Path)
    args = parser.parse_args()
    state = json.loads(args.state.read_text())
    origin = state['origin']
    url = urllib.parse.urlsplit(origin)
    scope = pathlib.Path('/tmp/light-media-plugin-scope-dRZTQ91A')
    data_dir = pathlib.Path(state['data_dir'])
    if (url.scheme != 'http' or url.hostname != '127.0.0.1' or not url.port
            or url.path or url.query or url.fragment
            or not data_dir.is_relative_to(scope / 'tmp')
            or not any(p.startswith('TestPluginWebMessagesProductionCLI') for p in data_dir.parts)):
        raise RuntimeError('state is not the exact owned loopback Web gate')
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

    def read(method, path, body=None, api_key=None):
        headers = {'Content-Type': 'application/json', 'Origin': origin}
        if api_key:
            headers['X-API-KEY'] = api_key
        request = urllib.request.Request(origin + path, method=method,
            data=json.dumps(body).encode() if body is not None else None, headers=headers)
        with client.open(request, timeout=12) as response:
            if response.status != 200:
                raise RuntimeError('owned read request failed')
            return json.load(response)

    read('POST', '/api/auth/login', {
        'username': 'web-message-acceptance',
        'password': os.environ.get('OWNED_WEB_PROBE_PASSWORD', 'owned-web-message-password'),
    })
    history = read('GET', '/api/message/web?count=-1')['data']
    events = read('GET', '/api/v1/plugin/OwnedInput/events')
    recipient_file = args.recipient_receipts or scope / 'messages42-pagination-sdk-production.json'
    recipient = {'direct_counter_available': False,
        'reason': 'held fixture has no read-only receiver endpoint; do not probe its POST-only sink'}
    if recipient_file.is_file():
        receipt = json.loads(recipient_file.read_text())
        if receipt.get('origin') == origin:
            recipient['actual_producer_receipt'] = receipt
        else:
            recipient['unrelated_receipt_not_combined'] = True
    result = {
        'kind': 'actual owned main HTTP history and actual SDK events; no mock, SSE or business mutation',
        'phase': args.phase,
        'observed_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'origin': origin,
        'server_sha256': 'f37e0d1205539e0a44adee61d616af0a7934b33939cbd21e494b42b0e1316d9e',
        'history_count': len(history), 'history': history,
        'sdk_input': events, 'recipient': recipient,
    }
    if args.previous:
        previous = json.loads(args.previous.read_text())
        old_ids = {row['id'] for row in previous['history']}
        new_ids = {row['id'] for row in history}
        old_sdk = previous['sdk_input']
        same_process = events['pid'] == old_sdk['pid']
        result['delta'] = {
            'same_origin': origin == previous['origin'],
            'added_history': [row for row in history if row['id'] not in old_ids],
            'removed_ids': sorted(old_ids - new_ids),
            'sdk_process_changed': not same_process,
            'new_sdk_events': (events.get('events') or [])[len(old_sdk.get('events') or []):] if same_process else None,
            'note': 'process-local SDK event arrays reset on real cold restart' if not same_process else None,
        }
    encoded = json.dumps(result, ensure_ascii=False, indent=2) + '\n'
    if args.out:
        args.out.write_text(encoded)
        print(json.dumps({'phase': args.phase, 'history_count': len(history),
            'sdk_pid': events['pid'], 'sdk_event_count': len(events.get('events') or []),
            'out': str(args.out)}, ensure_ascii=False))
    else:
        sys.stdout.write(encoded)


if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        # Never dump Request, headers, token, cookie or authentication response.
        print(json.dumps({'status': 'failed', 'error_type': type(exc).__name__,
                          'message': 'owned read-only observation failed; no secret body emitted'}), file=sys.stderr)
        raise SystemExit(1)
