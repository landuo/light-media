"""Offline control-flow oracle. No application import, network or user state.

Executes actual WordsMatcher and actual RSS/Search AST statements with synthetic
Context and a recording MetaInfo stub. The stub is deliberately NOT a parser
oracle; assertions cover invocation inputs, shallow copies and field retention.
"""
import ast
import argparse
import copy
import hashlib
import json
from dataclasses import dataclass, field
from functools import lru_cache
from pathlib import Path
from types import SimpleNamespace as NS
from typing import Optional, Set, Tuple

args = argparse.ArgumentParser()
args.add_argument('--source-root', type=Path, required=True,
                  help='Read-only extracted installed source root, containing app/; never import app')
args.add_argument('--output', type=Path, required=True)
args = args.parse_args()
ROOT = args.source_root
SOURCES = ['app/chain/subscribe.py', 'app/chain/search.py', 'app/chain/torrents.py',
           'app/core/metainfo.py', 'app/core/meta/words.py', 'app/core/context.py',
           'app/utils/rust_accel.py', 'app/core/meta/metabase.py',
           'app/modules/filter/__init__.py', 'app/helper/torrent.py']
source_sha = {p: hashlib.sha256((ROOT / p).read_bytes()).hexdigest() for p in SOURCES}
EXPECTED = {
    'app/chain/subscribe.py': '190f0500279b64f4ca2cd726f07dbb3e20ae9b7c46187268a208afcbe9c9b7ab',
    'app/chain/search.py': '2b04f6d246ea2573081cb72039a78acefb81cd7c895603e0433bab7dc7e7a528',
    'app/chain/torrents.py': '6c2833fc9a87e9d57881fcdef760d131dd1d36acbc9d5de3d7279cdbd0d96a47',
    'app/core/metainfo.py': '7c76f962f792a480615e0aa0ffd84fae16eecf62cae0ed3d8a0261f7283d59a9',
    'app/core/meta/words.py': '798759c94919636c94b690786b9d68a8b7f2f2815be611b04f7c857491d40277',
    'app/core/context.py': '70a088bd54c748ab44ec467e6a1f1fc5bb5368f818bb16288eca0d15cf98877e',
    'app/utils/rust_accel.py': '6c4c890639c0830d41a75d2e865eb3b3c3f3915b234d72976ea58bfbe5dc9dfc',
    'app/core/meta/metabase.py': '0156c105bf92991332a8742b9a4af4e1f3a74cf4b6f08107e0ed6de9d546f292',
    'app/modules/filter/__init__.py': '78f7741b8782ed23a8ef94a18afc14038366b832d9a0907cce9274664052abe8',
    'app/helper/torrent.py': '1c8a420d419ee53579079283ea0828536de9d978b6b15e722f1ba53cb51d5dd5',
}
assert source_sha == EXPECTED, 'Installed reference source changed; review before regenerating'

class Logger:
    def __getattr__(self, name):
        return lambda *args, **kwargs: None

values = {'global_words': ['Raw => Cached']}
class SystemConfig:
    def get(self, key):
        assert key == 'global_words'
        return values[key]

class RemoveAppImports(ast.NodeTransformer):
    def visit_ImportFrom(self, node):
        return None if node.module and node.module.startswith('app.') else node

words_tree = RemoveAppImports().visit(ast.parse((ROOT / 'app/core/meta/words.py').read_text()))
words_ns = {'SystemConfigOper': SystemConfig, 'SystemConfigKey': NS(CustomIdentifiers='global_words'),
            'Singleton': type, 'logger': Logger()}
exec(compile(ast.fix_missing_locations(words_tree), '<actual WordsMatcher AST>', 'exec'), words_ns)
WordsMatcher = words_ns['WordsMatcher']
matcher = WordsMatcher()

context_tree = ast.parse((ROOT / 'app/core/context.py').read_text())
context_class = next(n for n in context_tree.body if isinstance(n, ast.ClassDef) and n.name == 'Context')
context_ns = {'dataclass': dataclass, 'MetaBase': object, 'MediaInfo': object, 'TorrentInfo': object,
              'Optional': Optional, 'Set': Set}
exec(compile(ast.Module(body=[context_class], type_ignores=[]), '<actual Context AST>', 'exec'), context_ns)
Context = context_ns['Context']

subscribe_tree = ast.parse((ROOT / 'app/chain/subscribe.py').read_text())
rss_initializers = sorted([n for n in ast.walk(subscribe_tree) if isinstance(n, ast.Assign)
                           and n.lineno in (1867, 1868, 1869, 1870)], key=lambda n: n.lineno)
rss_if = next(n for n in ast.walk(subscribe_tree) if isinstance(n, ast.If) and n.lineno == 1879)
rss_code = compile(ast.fix_missing_locations(ast.Module(body=[*rss_initializers, rss_if], type_ignores=[])),
                   '<actual RSS Context-copy and rebuild AST>', 'exec')
search_tree = ast.parse((ROOT / 'app/chain/search.py').read_text())
search_assign = next(n for n in ast.walk(search_tree) if isinstance(n, ast.Assign) and n.lineno == 1242)
search_code = compile(ast.Module(body=[search_assign], type_ignores=[]), '<actual SearchChain MetaInfo AST>', 'exec')

class SyntheticMedia:
    def __init__(self):
        self.id = 918273
        self.marker = 'new-recognized-media'
        self.cleared = False
    def clear(self):
        self.cleared = True

cases = []
inputs = [
    ('none', None), ('direct_empty_list', []), ('empty_string_list', ['']),
    ('two_empty_lines', ['', '']), ('space_only', [' ']), ('comment_only', ['# Raw => Local']),
    ('invalid_regex', ['[']), ('unmatched_local', ['Unmatched => Local']),
    ('raw_only_rule', ['Raw => Local']), ('cached_only_rule', ['Cached => Local']),
    ('same_value_match', ['Cached => Cached']),
    ('combined_replace_but_failed_offset', ['Cached => Local && MISSING <> E >> EP+1']),
    ('successful_rule_then_failed_combined', ['Cached => Cached', 'Cached => Local && MISSING <> E >> EP+1']),
    ('raw_offset_once', ['E <> .1080p >> EP+1']),
]
for name, local in inputs:
    for recognize_success in (False, True):
        raw = 'Raw.S01E01.1080p.WEB-DL'
        cached_org, cached_applied = matcher.prepare(raw)
        # Arbitrary extra fields prove preservation/replacement of the entire
        # selected object, not just the five SubscribeGroup resource properties.
        cached_meta = NS(title=raw, org_string=cached_org, apply_words=cached_applied,
                         extra_sentinel={'nested': 'cached'}, begin_episode=501,
                         web_source='CachedPlatform', episode_group='CachedGroup')
        cached_media = NS(id=4, marker='cached-media')
        torrent = NS(title=raw, description='synthetic description', site_name='Synthetic Site')
        context = Context(meta_info=cached_meta, media_info=cached_media, torrent_info=torrent,
                          media_recognize_fail_count=2, resource_source='rss', match_source='cached-match',
                          candidate_recognized=True, media_info_is_target=False,
                          allowed_episodes={1, 3}, confirmed_full_coverage=True)
        calls = []
        def recording_meta(**kwargs):
            calls.append(copy.deepcopy(kwargs))
            return NS(title=kwargs['title'], org_string='new-meta-parser-stub',
                      extra_sentinel={'nested': 'new-meta'}, begin_episode=901)
        recognized = SyntheticMedia() if recognize_success else None
        class MediaChain:
            def recognize_by_meta(self, meta, **kwargs):
                assert meta is not cached_meta
                assert kwargs == {'episode_group': 'SubscriptionGroup', 'obtain_images': False}
                return recognized
        ns = dict(copy=copy, context=context, custom_words_list=local, wordsmatcher=matcher,
                  logger=Logger(), MetaInfo=recording_meta, MediaChain=MediaChain,
                  subscribe=NS(episode_group='SubscriptionGroup'),
                  resolve_media_identity=lambda media: ('tmdb', str(media.id)),
                  self=NS(__get_media_id_match_source=lambda media: 'new-id-match'))
        if local:
            prepared, applied = matcher.prepare(cached_org, custom_words=local)
        else:
            prepared, applied = None, None
        exec(rss_code, ns)
        selected = ns['_context']
        rebuilt = bool(calls)
        assert rebuilt == bool(local and applied)
        assert selected is not context
        assert selected.torrent_info is context.torrent_info
        assert selected.allowed_episodes is context.allowed_episodes
        assert selected.resource_source == 'rss' and selected.confirmed_full_coverage
        assert selected.media_recognize_fail_count == 2
        assert (selected.meta_info is cached_meta) != rebuilt
        assert context.meta_info is cached_meta and context.media_info is cached_media
        assert selected.media_info is (recognized if rebuilt and recognize_success else cached_media)
        if rebuilt:
            assert calls == [{'title': raw, 'subtitle': torrent.description, 'custom_words': local}]
        search_calls = []
        def record_search(**kwargs):
            search_calls.append(copy.deepcopy(kwargs))
            return NS(marker='new-search-meta')
        exec(search_code, {'MetaInfo': record_search, 'torrent': torrent, 'custom_words': local})
        assert search_calls == [{'title': raw, 'subtitle': torrent.description, 'custom_words': local}]
        cases.append(dict(name=name, local_words=local, recognize_success=recognize_success,
                          cached_org_string=cached_org, rss_prepared_probe=prepared,
                          rss_probe_apply_words=applied, rss_rebuilt=rebuilt,
                          rss_parser_calls=calls, rss_kept_meta_identity=selected.meta_info is cached_meta,
                          rss_kept_media_identity=selected.media_info is cached_media,
                          rss_match_source=selected.match_source,
                          rss_candidate_recognized=selected.candidate_recognized,
                          rss_media_info_is_target=selected.media_info_is_target,
                          rss_original_cached_context_unchanged=True,
                          rss_context_allowed_episodes_shared=True,
                          search_parser_calls=search_calls))

# Execute actual Python option-collector functions; the Rust engine itself is
# intentionally not replaced with our own implementation or claimed verified.
meta_tree = ast.parse((ROOT / 'app/core/metainfo.py').read_text())
option_functions = [n for n in meta_tree.body if isinstance(n, ast.FunctionDef)
                    and n.name in ('_rust_custom_parse_options', '_rust_parse_options')]
options_ns = {'lru_cache': lru_cache, 'Tuple': tuple, 'List': list,
              '_rust_default_parse_options': lambda: {'custom_words': values['global_words'],
                  'release_groups': ['sentinel-group'], 'customization': ['sentinel-customization'],
                  'media_exts': ['.mkv'], 'streaming_platforms': {'SYN': 'synthetic'}},
              '_rust_parse_options_cache_key': lambda options: 'synthetic-cache-key',
              '_RUST_PARSE_OPTIONS_CACHE_KEY': '_synthetic_cache_key'}
exec(compile(ast.Module(body=option_functions, type_ignores=[]), '<actual Rust option collector AST>', 'exec'), options_ns)
mode_cases = []
for name, local in inputs:
    python_prepared, python_applied = matcher.prepare('Raw.S01E01.1080p.WEB-DL', custom_words=local)
    rust_options = options_ns['_rust_parse_options'](local)
    mode_cases.append(dict(name=name, local_words=local,
                           legacy_actual_words_result=dict(title=python_prepared, applied=python_applied),
                           rust_actual_option_words=rust_options['custom_words'],
                           rust_preserves_other_option_fields=all(k in rust_options for k in
                               ['release_groups', 'customization', 'media_exts', 'streaming_platforms'])))

# Actual filter AST proves its independent MetaInfo/default-options invocation;
# synthetic total_episode intentionally avoids pretending to parse core identity.
filter_tree = ast.parse((ROOT / 'app/modules/filter/__init__.py').read_text())
match_size = next(n for n in ast.walk(filter_tree) if isinstance(n, ast.FunctionDef) and n.name == '__match_size')
match_size = copy.deepcopy(match_size)
match_size.decorator_list = []
parse_size = next(n for n in filter_tree.body if isinstance(n, ast.FunctionDef) and n.name == '_parse_size_range')
metainfo_options = next(n.value for n in ast.walk(filter_tree) if isinstance(n, ast.keyword) and n.arg == 'metainfo_options')
size_calls = []
def size_meta(**kwargs):
    size_calls.append(copy.deepcopy(kwargs))
    return NS(total_episode=2)
filter_ns = {'TorrentInfo': object, 'Tuple': Tuple, 'Optional': Optional,
             'lru_cache': lru_cache, '_SIZE_UNIT': 1024 * 1024, 'MetaInfo': size_meta}
exec(compile(ast.Module(body=[parse_size, match_size], type_ignores=[]), '<actual priority size AST>', 'exec'), filter_ns)
size_torrent = NS(title='Raw.S04E01-E04.1080p.WEB-DL', description='synthetic raw description', size=4 << 30)
assert filter_ns['__match_size'](size_torrent, '>1500') is True
assert size_calls == [{'title': size_torrent.title, 'subtitle': size_torrent.description}]
filter_options = eval(compile(ast.Expression(body=metainfo_options), '<actual Rust filter options AST>', 'eval'),
                      {'_rust_parse_options': options_ns['_rust_parse_options'],
                       'self': NS(__needs_metainfo_options=lambda groups: True), 'group_defs': []})
assert filter_options['custom_words'] == values['global_words']
priority_scope = dict(legacy_meta_calls=size_calls, synthetic_total_episode=2,
                      synthetic_size_range='>1500', synthetic_size_bytes=4 << 30,
                      rust_filter_option_words=filter_options['custom_words'],
                      raw_rule_fields='TorrentInfo.title/description; no local Context passed',
                      sort_scope='helper/torrent.py:301 uses existing Context.meta_info, no reparse')

output = dict(scope='Actual original RSS/Search AST control flow with synthetic recording MetaInfo; actual Python WordsMatcher and Rust option collector; NOT an end-to-end MetaInfo or media-recognition oracle',
              source_root='Installed MoviePilot /app; read-only named Python source extraction; no application imports or user configuration', source_sha256=source_sha,
              distributions={'regex': __import__('importlib.metadata', fromlist=['version']).version('regex'),
                             'cn2an': __import__('importlib.metadata', fromlist=['version']).version('cn2an')},
              rss_cases=cases, source_mode_option_cases=mode_cases, priority_scope=priority_scope)
args.output.write_text(json.dumps(output, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'rss_cases': len(cases), 'source_mode_option_cases': len(mode_cases), 'priority_scope': 'actual AST invocation checked',
                  'output_sha256': hashlib.sha256(args.output.read_bytes()).hexdigest()}, indent=2))
