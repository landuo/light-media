#!/usr/bin/env python3
"""Reproduce the work-only, synthetic anitomy-pure pipeline oracle.

Python is offline generation tooling. The Go product/draft runtime does not use it.
Only the pinned crate source, public source tests and synthetic strings are read.
"""
import hashlib
import itertools
import json
import gzip
import random
import re
import shutil
import subprocess
from pathlib import Path

TOOL = Path(__file__).resolve().parent
HERE = TOOL
ROOT = HERE.parents[2]
DRAFT = ROOT / "work/_releasemeta-rust-port"
# Supply an unpacked, checksum-verified original crate with --source.
import argparse
_args=argparse.ArgumentParser()
_args.add_argument("--source",type=Path,default=ROOT / "work/releasemeta-source/anitomy-pure-0.1.0")
args=_args.parse_args()
SOURCE=args.source.resolve()
HERE=ROOT / "work/_releasemeta-rust-reference"
HERE.mkdir(parents=True,exist_ok=True)


def build_reference():
    shutil.copytree(SOURCE / 'src', HERE / 'src', dirs_exist_ok=True)
    hashes = {str(p.relative_to(SOURCE / 'src')): hashlib.sha256(p.read_bytes()).hexdigest()
              for p in sorted((SOURCE / 'src').rglob('*.rs'))}
    recorded=json.loads(gzip.decompress((ROOT/'testdata/plugins/release-meta-rust-anitomy-oracle.json.gz').read_bytes()))
    assert hashes==recorded['source_sha256'], 'The supplied source differs from the recorded pinned crate'
    assert subprocess.check_output(['rustc','--version'],text=True).strip()==recorded['rustc'], 'Use the recorded Rust toolchain for reproducible oracle generation'
    (HERE / 'original-sha256.json').write_text(json.dumps(hashes, indent=2) + '\n')
    path = HERE / 'src/elements.rs'
    text = path.read_text()
    position = text.index('impl Elements {') + len('impl Elements {')
    text = text[:position] + '\n    /// Offline read-only access to insertion order.\n    pub fn reference_values(&self) -> &[Element] { &self.elements }\n' + text[position:]
    path.write_text(text)
    path = HERE / 'src/lib.rs'
    text = path.read_text()
    start = text.index('    pub fn parse(&self)')
    brace = text.index('{', start)
    depth = 1
    end = brace + 1
    while depth:
        depth += (text[end] == '{') - (text[end] == '}')
        end += 1
    copied = text[start:end]
    copied = copied.replace('pub fn parse(&self) -> Result<Elements, ParsingError>', 'pub fn reference_parse(&self) -> Result<(Elements, Vec<String>), ParsingError>')
    copied = copied.replace('return Ok(found_elements)', 'return Ok((found_elements, Vec::new()))')
    copied = copied.replace('let mut tokens = split_raw_data(', 'let mut stages = Vec::new();\n        let mut tokens = split_raw_data(')
    markers = [
        ('        parsing_keywords(&mut found_elements, &mut tokens);', 'keywords'),
        ('            parse_episode_number(&self.allowed_delimiters, &mut tokens, &mut found_elements);', 'episode'),
        ('            detect_episode_number_alt(&mut tokens, &mut found_elements);', 'episode_alt'),
        ('        parse_anime_title(&mut tokens, &mut found_elements, &self.allowed_delimiters);', 'anime_title'),
        ('            parse_release_group(&mut tokens, &mut found_elements, &self.allowed_delimiters);', 'release_group'),
        ('            parse_episode_title(&mut tokens, &mut found_elements, &self.allowed_delimiters);', 'episode_title'),
    ]
    copied = copied.replace(markers[0][0], '        stages.push(reference_snapshot("split", &found_elements, &mut tokens));\n' + markers[0][0])
    for marker, phase in markers:
        assert copied.count(marker) == 1
        copied = copied.replace(marker, marker + '\n        stages.push(reference_snapshot("' + phase + '", &found_elements, &mut tokens));')
    copied = copied.replace('Ok(found_elements)', 'Ok((found_elements, stages))')
    text = text[:end] + '\n    /// Offline instrumented parse with original mutation order.\n' + copied + text[end:]
    path.write_text(text + (TOOL / 'harness.rs').read_text())
    subprocess.run(['rustc', '--edition=2024', '-Awarnings', str(path), '-o', str(HERE / 'parser')], check=True)
    return hashes


def corpus():
    seen = set()
    cases = []

    def add(name, title, **options):
        key = title, json.dumps(options, sort_keys=True)
        if key not in seen:
            seen.add(key)
            cases.append(dict(name=name, title=title, options=options))

    # Every literal Parser::new input in the exact pinned crate's source/tests.
    # Rust test literals here use JSON-compatible escaping (assert decoding).
    pattern = re.compile(r'(?:Parser::new|get_extension|remove_extension|parse_single_ep|match_multiple_ep|match_season_ep_patern|match_fractal_episode|match_japanese_counter|match_number_sign|match_partial_episode)\(\s*("(?:[^"\\]|\\.)*")', re.S)
    for file in sorted(SOURCE.rglob('*.rs')):
        for index, match in enumerate(pattern.finditer(file.read_text())):
            add('crate/' + str(file.relative_to(SOURCE)) + '/' + str(index), json.loads(re.sub(r'\\x([0-9a-fA-F]{2})',r'\\u00\1',match[1])))
    installed = json.loads((ROOT / 'testdata/plugins/release-meta-original-oracle.json').read_text())
    for item in installed['cases']:
        add('outer-synthetic/' + item['name'], item['title'])
    episodes = ['01', '01v2', '01-03', '01v2-03v3', 'S04E01', 'S04-S05E01-E03',
                'S04E01-E03', '4x01', '4x01-4x03', 'E01', 'EP01', 'EP1.5', '#01-03',
                '#03-01', '1.5', '001A', '第12話', '第12集', 'OVA02', 'SP03', 'VOL04',
                '1999', '720', '480', '1080', '02v1v2', '++1', '2147483648',
                '9999999999999', '01 & 02', '01+03', '01 of 12', '01 (13)', '01 (001)',
                '01-00', '٠١', '１２', '²', 'Ⅳ']
    tech = ['', '[1080p]', '[Hi10P FLAC]', '[BDRip 1920x1080 H.264 AAC 5.1]',
            '[E-AC-3 DTS-ES]', '[WEB-DL HEVC Dual Audio]', '(VOSTFR)',
            '[English Subbed]', '[TV]', '(OVA)', '[720p480p]', '[CRC32]', '[A1B2C3D4]']
    groups = ['', '[ExampleSubs] ', '(ExampleSubs) ', '「ExampleSubs」 ', '[TV] ', '[G] ']
    for n, (ep, suffix, group) in enumerate(itertools.product(episodes, tech, groups)):
        add('grammar/' + str(n), group + 'Example Show - ' + ep + ' ' + suffix + '.mkv')
    edges = ['', '.mkv', '.hidden', 'foo.MKV', 'foo.mkv', 'foo.zip', '[foo', '[foo.mkv',
             '[a(b]c)d', '[a]b[c', 'a___b...c---d', 'a...720...b', '[a][b][c]',
             '[]', '()', '{}', '「」', '【】', 'a\nb\tc', '[a\\b] - 01 [4k].mkv',
             'Show[1][2][3]', 'Show [Episode 01] [Season 2]', 'Show Second Season 01',
             'Show S04 E02 10bit x265-GROUP', 'Show - 01 BY GROUP',
             'Show - 01 (2014) [Group]', 'Ⅰ - 01', 'ᢅ - 01', '𑻠 - 01', '𐐨 - 01',
             'ᾀ - 01', 'ß - 01', 'ǰ - 01', 'ſ - 01', 'ı - 01', 'İ - 01',
             'foo.mkv/', 'foo.mkv/.', 'foo.mkv//./.', 'foo.mkv/..',
             'foo.mkv/../', '.mkv/', './foo.mkv/./', '../foo.mkv/',
             'foo\\bar.mkv', '/foo/.mkv', '/foo/..mkv', 'foo/.']
    for n, title in enumerate(edges):
        add('edge/' + str(n), title)
    keyword_pattern = re.compile(r'\.add\(\s*"((?:[^"\\]|\\.)*)"', re.S)
    source_keywords = [json.loads('"' + re.sub(r'\\x([0-9a-fA-F]{2})',r'\\u00\1',m[1]) + '"') for m in keyword_pattern.finditer((SOURCE/'src/keyword.rs').read_text())]
    for n, keyword in enumerate(source_keywords):
        for context, title in enumerate([
            keyword, '[%s] Example Show - 01.mkv' % keyword,
            'Example Show - 01 [%s].mkv' % keyword,
            'Example Show - 01 (%s).mkv' % keyword,
            'Example Show - 01 %s.mkv' % keyword,
            '[%s unknown] Example Show - 01.mkv' % keyword,
            '[%s JAP FR 1080p] Example Show - 01.mkv' % keyword.lower(),
        ]):
            add('keyword/%s/%s' % (n,context), title)
    # Deterministic grammar exploration, including malformed bracket/delimiter
    # topology. These are synthetic parser inputs, not user file names.
    rng = random.Random(0xA1700)
    pieces = ['Show','Title','Group','Group-Name','ep','Episode','S4','Season','2nd',
              '04','2010','00','01','13','03v2','1.5','#03','#01-03','S04E03',
              '4x01','S02-S04E01-E12','OVA01','SP02','OP3','VOL2','720',
              '480','1080','4k','720p','1280x720','1080p480p','x264','H.264',
              'H','264','DTS','ES','E','AC','3','AAC','5','1','FLAC','WEB','DL',
              'BD','BDRip','Blu-Ray','HDTV','US','JP','JAP','ENG','VOSTFR',
              'English','Subbed','Complete','v2','BY','1280×720','A1B2C3D4',
              '第12話','番组','Ⅰ','ᢅ','𑻠','𐐨','İ','ß','ſEASON','+1','++1',
              '123456789012','02-01','-','~','&','+','!',"'",'🔥']
    joiners = [' ','_','.', '-', ' & ',' + ',' | ','___','...','---','']
    brackets = [('', ''),('[', ']'),('(', ')'),('{', '}'),('「', '」'),('【', '】'),('[', ')'),('(', ']')]
    for n in range(5000):
        chunks=[]
        for _ in range(rng.randint(1,5)):
            inside=rng.choice(joiners).join(rng.choices(pieces,k=rng.randint(1,7)))
            left,right=rng.choice(brackets)
            chunks.append(left+inside+right)
        title=rng.choice(joiners).join(chunks)+rng.choice(['','.mkv','.mp4','.Mkv','.zip'])
        add('seeded-grammar/'+str(n),title)
    title = '[Group] Example_Show - S04E01v2 - Episode_Title [BD 1080p H.264 AAC].mkv'
    for flags in itertools.product([False, True], repeat=4):
        add('flags/' + ''.join(str(int(x)) for x in flags), title,
            episode_number=flags[0], episode_title=flags[1], release_group=flags[2], file_extension=flags[3])
    for delimiter in ['', ' ', '_', '.', '-', ' ._', '🔥', ' _.-&+,|']:
        add('delimiter/' + delimiter, title, allowed_delimiters=delimiter)
    for ignored in [['Group'], [title], ["['Group', 'Example']"], ['["Group", "Example"]'], ['']]:
        add('ignored/' + str(ignored), title, ignored_strings=ignored)
    return cases


def main():
    hashes = build_reference()
    cases = corpus()
    lines = []
    for case in cases:
        options = case['options']
        flags = ''.join('1' if options.get(k, True) else '0' for k in ['episode_number', 'episode_title', 'release_group', 'file_extension'])
        delimiter = options.get('allowed_delimiters')
        delimiter = '-' if delimiter is None else delimiter.encode().hex()
        ignored = options.get('ignored_strings', [])
        ignored = ','.join(s.encode().hex() for s in ignored) if ignored else '-'
        lines.append(case['title'].encode().hex() + '\t' + flags + '\t' + delimiter + '\t' + ignored)
    result = subprocess.run([str(HERE / 'parser')], input='\n'.join(lines) + '\n', text=True, capture_output=True, check=True)
    outputs = result.stdout.splitlines()
    assert len(outputs) == len(cases), (len(outputs), len(cases))
    for case, output in zip(cases, outputs):
        parsed = json.loads(output)
        case.update(error=parsed.pop('error', ''), result=parsed)
    data = dict(source='anitomy-pure 0.1.0 exact crate, instrumented stages, original final result asserted equal',
                crate_checksum='33f525032668d2aff5dff115958157db7aecf1dc2fd5f5df93cf1be1452dfd4a',
                rustc=subprocess.check_output(['rustc', '--version'], text=True).strip(),
                source_sha256=hashes, cases=cases)
    count=0
    for case in cases:
        name=case['name']
        trace=name.startswith(('crate/','edge/','flags/','delimiter/','ignored/')) or name.startswith('keyword/') and name.rsplit('/',1)[1] in ('3','6') or name.startswith('grammar/') and (int(name.rsplit('/',1)[1])<20 or int(name.rsplit('/',1)[1])%97==0) or name.startswith('seeded-grammar/') and int(name.rsplit('/',1)[1])<100
        if trace and case['result'].get('stages'):count+=1
        else:case['result'].pop('stages',None)
    data['unicode_version']='17.0.0'
    data['trace_selection']='All upstream Parser/helper literals, edge/builder inputs, keyword parentheses and mixed-resource contexts, first 20 + every 97th Cartesian grammar, first 100 seeded grammar; every case retains full ordered final elements/error.'
    data['trace_cases']=count
    path = ROOT / 'testdata/plugins/release-meta-rust-anitomy-oracle.json.gz'
    payload=(json.dumps(data, ensure_ascii=False, separators=(',', ':')) + '\n').encode()
    path.write_bytes(gzip.compress(payload,mtime=0))
    print(json.dumps(dict(cases=len(cases), errors=sum(bool(c['error']) for c in cases), bytes=path.stat().st_size,
                          sha256=hashlib.sha256(path.read_bytes()).hexdigest())))


if __name__ == '__main__':
    main()
