"""Offline SHA manifest; never imports installed product or reads user data."""
import pathlib,hashlib,json
root=pathlib.Path(__file__).parent.parent
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
std=pathlib.Path('/opt/homebrew/Cellar/go/1.26.2/libexec/src/image/jpeg')
metadata={
 'status':'isolated candidate; not formal or deployed; 8-bit Huffman DCT slice only',
 'libjpeg_turbo':{'version':'3.1.4.1','commit':'9217719d3a58633923b096af4c1d50d304768a64','base_url':'https://raw.githubusercontent.com/libjpeg-turbo/libjpeg-turbo/9217719d3a58633923b096af4c1d50d304768a64/','sources':{p.name:sha(p) for p in sorted((root/'reference').glob('src_*'))}|{p.name:sha(p) for p in sorted((root/'reference').glob('simd_*'))}|{p.name:sha(p) for p in [root/'reference/LICENSE.md',root/'reference/README.ijg']}},
 'pillow':{'version':'12.2.0','commit':'3c41c095064200a02672d89cc5ff629eaf4b0d4f','base_url':'https://raw.githubusercontent.com/python-pillow/Pillow/3c41c095064200a02672d89cc5ff629eaf4b0d4f/','sources':{p.name:sha(p) for p in sorted((root/'reference').glob('pillow_*'))}|{'Pillow-LICENSE':sha(root/'reference/Pillow-LICENSE')}},
 'go_base':{'version':'go1.26.2','base_url':'https://raw.githubusercontent.com/golang/go/go1.26.2/src/image/jpeg/','source_sha256':{p.name:sha(p) for p in [std/'reader.go',std/'scan.go',std/'huffman.go',std/'dct.go']},'license_sha256':sha(root/'jpegport/LICENSE')},
 'installed_binary_provenance_sha256':sha(root/'reference/installed-binary-provenance.json'),
 'parent_hash_copy':{'source':'../_tjupt_imagehash_port/hash.go','sha256':sha(root/'hash.go'),'source_tjupt_sha256':'ed717891d30abcac798407c8889776ec79191e447b4ef7f97aee1bddb0f98bb0','source_hash_methods_ast_sha256':'8298a5ba26f3156e1f83fdf318af3d557c04b34f312b4f9f9781dd9e6a15d8df'},
 'original_oracles':{p.name:sha(p) for p in sorted((root/'testdata').glob('*.gz'))},
 'offline_generators':{p.name:sha(p) for p in sorted((root/'reference').glob('generate*.py'))},
 'runtime_go':{str(p.relative_to(root)):sha(p) for p in sorted(root.rglob('*.go')) if not p.name.endswith('_test.go')},
 'tests_go':{str(p.relative_to(root)):sha(p) for p in sorted(root.rglob('*_test.go'))},
}
(root/'reference/source-checksums.json').write_text(json.dumps(metadata,indent=2,sort_keys=True)+'\n')
print(json.dumps({'manifest_sha256':sha(root/'reference/source-checksums.json'),'pillow_jpeg_plugin_installed_equals_fixed':metadata['pillow']['sources']['pillow_src_PIL_JpegImagePlugin.py']==json.loads((root/'reference/installed-binary-provenance.json').read_text())['pillow_sources']['JpegImagePlugin.py']},indent=2))
