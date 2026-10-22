#!/usr/bin/env python3
"""Candidate offline oracle. Not production code; explicit host/schema stubs below.
Offline reproduction: python3 generate.py --source /path/to/pinned/site.py > oracle.json
Only the three pinned SiteChain methods provide the compared algorithm.
"""
import argparse
import ast
import copy
import hashlib
import json
from pathlib import Path
import re
from types import SimpleNamespace
from typing import Optional, Dict, Callable

SHA = '06358a8fd070d46ab254015a2f8c7a34f07c35dd240bd2b3fef38cbf219252fa'
UPSTREAM = '1528176beacc0f9f8d8d9cbf1adf048b0da67b5c'
METHODS = ('refresh_userdata', '_post_site_messages', 'refresh_userdatas')

class SyntheticPostError(RuntimeError):
    pass

class SyntheticLookupError(RuntimeError):
    pass

class DTO(SimpleNamespace):
    def model_dump(self):
        return copy.deepcopy(vars(self))

class NotificationStub:
    """Records explicit constructor kwargs ONLY, not Pydantic validation/defaults."""
    def __init__(self, **kwargs):
        self.kwargs = copy.deepcopy(kwargs)

class Host:
    def __init__(self, spec):
        self.spec = copy.deepcopy(spec)
        self.trace = []
        self.posts = 0
        self.lookups = 0
        self.history = list(spec.get('history', []))
        self.globals = SimpleNamespace(is_system_stopped=spec.get('stopped', False))
        self.sites = copy.deepcopy(spec.get('sites', [site()]))

    def record(self, op, **kwargs):
        self.trace.append(dict(op=op, **copy.deepcopy(kwargs)))

    def run_module(self, method, site):
        self.record('run_module', method=method, site=site['id'])
        source = self.spec.get('by_site', {}).get(site['id'], self.spec.get('dto', {}))
        if source is None:
            return None
        if source == 'raise':
            raise SyntheticPostError('synthetic module exception')
        return DTO(**dict({'message_unread': 0, 'message_unread_contents': [],
                           'ratio': 2.0, 'user_level': 'User'}, **copy.deepcopy(source)))

    def update_userdata(self, **kwargs):
        self.record('save_userdata', **kwargs)

    def send_event(self, event, data):
        self.record('event', event=event, data=data)

    def exists_by_source(self, source):
        self.lookups += 1
        self.record('exists_by_source', source=source)
        if self.lookups == self.spec.get('lookup_error_at'):
            raise SyntheticLookupError('synthetic history read exception')
        # EXPLICIT STUB: equality against a synthetic list, NOT MessageOper proof.
        return source in self.history

    def post_message(self, message):
        self.posts += 1
        self.record('post_call', ordinal=self.posts, kwargs=message.kwargs)
        if self.posts == self.spec.get('post_error_at') and not self.spec.get('error_after_history'):
            raise SyntheticPostError('synthetic post exception')
        if self.spec.get('simulate_history', True):
            self.record('host_history_stub', kwargs=message.kwargs)
            if message.kwargs.get('source'):
                self.history.append(message.kwargs['source'])
        if self.posts == self.spec.get('post_error_at'):
            raise SyntheticPostError('synthetic post exception after history')
        if self.spec.get('stop_after_post') == self.posts:
            self.globals.is_system_stopped = True
        value = self.spec.get('post_return')
        self.record('post_return', value=value)
        return value

    def progress(self, **kwargs):
        self.record('progress', **kwargs)


def site(identifier='a', active=True):
    return {'id': identifier, 'name': 'Synthetic ' + identifier,
            'domain': 'synthetic-' + identifier + '.invalid',
            'url': 'https://synthetic-' + identifier + '.invalid/', 'is_active': active}


def cases():
    out = []
    def add(name, **spec):
        out.append(dict(name=name, **spec))
    row = ['head', '2026-01-02', 'body']
    for n in (0, None, False, -1, 1, 3):
        add('unread-' + str(n), dto={'message_unread': n})
    for value in (None, False, True):
        add('summary-return-' + str(value), dto={'message_unread': 2}, post_return=value)
    rows = {
        'three-items': [row], 'four-items': [row + ['source-a']],
        'more-items': [row + ['source-a', 'ignored', {'unused': 1}]],
        'null-fields': [[None, None, None]], 'false-source': [row + ['']],
        'null-source': [row + [None]], 'same-no-source': [row, row],
        'same-source': [row + ['source-a'], row + ['source-a']],
        'different-source': [row + ['source-a'], row + ['source-b']],
        'malformed-short': [['head', 'date']],
        'later-malformed-short': [row, ['head']],
        'later-null-row': [row, None],
        'later-number-row': [row, 7],
        'string-row-iterable': ['abc'],
        'dict-row-iterable': [{'head': 1, 'date': 2, 'body': 3}],
    }
    for name, values in rows.items():
        add(name, dto={'message_unread': 2, 'message_unread_contents': values})
    add('existing-source', history=['source-a'], dto={'message_unread': 2,
        'message_unread_contents': [row + ['source-a'], row + ['source-b']]})
    for ratio in (None, False, 0, -0.5, 0.5, 1, 1.5, '0', '0.5', 'bad'):
        add('ratio-' + repr(ratio), dto={'ratio': ratio})
    for level in (None, '', 'User', 'VIP', 'vip', 'VI', 'Vİ', 'Vı', 'V', '贵宾', 'preVIpost'):
        add('level-' + repr(level), dto={'ratio': 0.5, 'user_level': level})
    for error_at in (1, 2, 3):
        for after in (False, True):
            add('post-error-%s-after-%s' % (error_at, after), post_error_at=error_at,
                error_after_history=after, dto={'ratio': 0.5, 'message_unread': 2,
                    'message_unread_contents': [row + ['source-a'], row + ['source-b']]})
    add('lookup-error', lookup_error_at=1, dto={'message_unread': 1,
        'message_unread_contents': [row + ['source-a']]})
    add('no-dto', dto=None)
    add('module-raises', dto='raise')
    batch_sites = [site('a'), site('disabled', False), site('b')]
    add('batch-normal', batch=True, sites=batch_sites, by_site={'a': {'message_unread': 1}, 'b': {'ratio': 0.5}})
    add('batch-none', batch=True, sites=batch_sites, dto=None)
    add('batch-partial', batch=True, sites=batch_sites, by_site={'a': None, 'b': {}})
    add('batch-post-error', batch=True, sites=batch_sites, post_error_at=1, dto={'message_unread': 1})
    add('batch-second-post-error', batch=True, sites=batch_sites, post_error_at=2, dto={'message_unread': 1})
    add('batch-stopped', batch=True, sites=batch_sites, stopped=True)
    add('batch-stop-after-post', batch=True, sites=batch_sites, stop_after_post=1, dto={'message_unread': 1})
    add('batch-empty', batch=True, sites=[])
    return out


def schema_scope(spec):
    """Labels DTO boundary only. No claim that the host stub validates models."""
    dto = spec.get('dto', {})
    if dto == 'raise':
        return {'kind': 'module_exception', 'reason': 'Synthetic module outcome, not a DTO value.'}
    if isinstance(dto, dict):
        ratio = dto.get('ratio')
        if ratio == 'bad':
            return {'kind': 'invalid_schema', 'reason': 'Optional[float] rejects the nonnumeric string bad; the unvalidated host stub reaches float() instead.'}
        if isinstance(ratio, str):
            return {'kind': 'schema_coercion_not_applied', 'reason': 'Numeric strings are accepted and normalized to float by SiteUserData; this host DTO bypasses that normalization, so its truthiness/text trace is not schema evidence.'}
    return {'kind': 'schema_representable', 'reason': 'DTO fields are representable by the pinned schema; null/bool numeric cases exercise equivalent false branches, not complete schema coercion fidelity.'}


def main():
    parser = argparse.ArgumentParser(description="Reproduce the pinned SiteChain offline oracle")
    parser.add_argument("--source", type=Path, required=True, help="Exact pinned app/chain/site.py; SHA256 is verified before AST execution")
    source = parser.parse_args().source
    raw = source.read_bytes()
    if hashlib.sha256(raw).hexdigest() != SHA:
        raise SystemExit('pinned source SHA mismatch')
    text = raw.decode('utf-8')
    tree = ast.parse(text)
    cls = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'SiteChain')
    found = {n.name: n for n in cls.body if isinstance(n, ast.FunctionDef) and n.name in METHODS}
    if set(found) != set(METHODS) or any(n.decorator_list for n in found.values()):
        raise SystemExit('unexpected method structure/decorators')
    methods = ast.Module(body=[copy.deepcopy(found[name]) for name in METHODS], type_ignores=[])
    code = compile(ast.fix_missing_locations(methods), str(source), 'exec')
    result = []
    for spec in cases():
        host = Host(spec)
        namespace = {'Optional': Optional, 'Dict': Dict, 'Callable': Callable,
                     'SiteUserData': DTO, 'Notification': NotificationStub,
                     'NotificationType': SimpleNamespace(SiteMessage='SiteMessage'),
                     'EventType': SimpleNamespace(SiteRefreshed='SiteRefreshed'),
                     'SiteOper': lambda: host, 'SitesHelper': lambda: SimpleNamespace(get_indexers=lambda: host.sites),
                     'StringUtils': SimpleNamespace(get_url_domain=lambda value: value),
                     'eventmanager': host, 'global_vars': host.globals, 're': re}
        exec(code, namespace)
        instance = SimpleNamespace(run_module=host.run_module, post_message=host.post_message, messageoper=host)
        for name in METHODS:
            setattr(instance, name, namespace[name].__get__(instance))
        value, error = None, None
        try:
            value = instance.refresh_userdatas(progress_callback=host.progress) if spec.get('batch') else instance.refresh_userdata(host.sites[0])
            if isinstance(value, DTO):
                value = value.model_dump()
            elif isinstance(value, dict):
                value = {k: v.model_dump() for k, v in value.items()}
        except Exception as exc:
            error = {'class': type(exc).__name__, 'message': str(exc)}
        result.append({'input': spec, 'schema_scope': schema_scope(spec), 'trace': host.trace, 'result': value,
                       'error': error, 'host_stub_history': host.history})
    print(json.dumps({'source': {'repository': 'jxxghp/MoviePilot', 'commit': UPSTREAM,
                                'path': 'app/chain/site.py', 'sha256': SHA,
                                'methods': {k: {'start': v.lineno, 'end': v.end_lineno} for k, v in found.items()}},
                      'limitations': ['Host dependencies are synthetic stubs, not their source implementations.',
                        'MessageOper source lookup/storage semantics not proven.',
                        'Notification constructor records explicit kwargs; schema validation/defaults not proven.',
                        'StringUtils domain conversion is identity for already-normalized synthetic domains.',
                        'Synthetic errors test source control flow, not real transport or module error classification.',
                        'No real accounts, network, database, production sends, or real parser integration.'],
                      'case_count': len(result), 'cases': result}, ensure_ascii=False, indent=2))

if __name__ == '__main__':
    main()
