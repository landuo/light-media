#!/usr/bin/env python3
"""Read-only exhaustive comparison with the NAS wheel compiler's official tables.

No app import, credentials, service mutation, or Go build. Rust is offline
verification only; the application's runtime remains Go.
"""
import argparse,bisect,hashlib,json,re,subprocess,urllib.request
from pathlib import Path
COMMIT="8bab26f4f68e0e26f0bb7960be334d5b520ea452"
SOURCES={
 "src/version":"ddf93c2b5a907811af43f301345fce8f5eb8a96fa60f3e8f2c17e1b3904e6181",
 "library/core/src/unicode/unicode_data.rs":"4438831f7c584a35457022c4016ab8a295f242ff809b81a5c81b9bb14dc5e166",
 "library/core/src/char/methods.rs":"6794677490411e762ee66df96438a112fb6ca5bdc0a1ae11a9933e93eee77909",
}
RUNNER='pub use std::{ops,cmp};\n#[path="unicode_module.rs"]mod data;\nuse std::io::{self,Write};\nfn mapped(v:[char;3])->String {match v{[a,\'\\0\',_]=>a.to_string(),[a,b,\'\\0\']=>[a,b].into_iter().collect(),[a,b,c]=>[a,b,c].into_iter().collect()}}\nfn main(){let mut out=io::BufWriter::new(io::stdout());writeln!(out,"version\\t{}.{}.{}",data::UNICODE_VERSION.0,data::UNICODE_VERSION.1,data::UNICODE_VERSION.2).unwrap();\nfor n in 0..=0x10ffff {if let Some(c)=char::from_u32(n){\nlet alpha=match c{\'a\'..=\'z\'|\'A\'..=\'Z\'=>true,\'\\0\'..=\'\\u{A9}\'=>false,_=>data::alphabetic::lookup(c)};\nlet numeric=match c{\'0\'..=\'9\'=>true,\'\\0\'..=\'\\u{B1}\'=>false,_=>data::n::lookup(c)};\nlet upper=match c{\'A\'..=\'Z\'=>true,\'\\0\'..=\'\\u{BF}\'=>false,_=>data::uppercase::lookup(c)};\nlet flags=(alpha as u8)|((numeric as u8)<<1)|((upper as u8)<<2);\nwriteln!(out,"{:x}\\t{}\\t{}\\t{}",n,flags,mapped(data::conversions::to_upper(c)).chars().map(|v|format!("{:x}",v as u32)).collect::<Vec<_>>().join(","),mapped(data::conversions::to_lower(c)).chars().map(|v|format!("{:x}",v as u32)).collect::<Vec<_>>().join(",")).unwrap();\n}}}\n'

def main():
 parser=argparse.ArgumentParser()
 parser.add_argument("--source-dir",type=Path)
 parser.add_argument("--root",type=Path)
 parser.add_argument("--fetch",action="store_true",help="Fetch only the three pinned public Rust source files")
 args=parser.parse_args()
 root=(args.root or Path(__file__).resolve().parents[3]).resolve()
 scratch=root/"work/_releasemeta-installed-unicode-verification"
 scratch.mkdir(parents=True,exist_ok=True)
 source_dir=args.source_dir or root/"work/releasemeta-installed-rust-stdlib"
 for path,sha in SOURCES.items():
  target=source_dir/path.replace("/","__")
  if args.fetch:
   source_dir.mkdir(parents=True,exist_ok=True)
   raw=urllib.request.urlopen("https://raw.githubusercontent.com/rust-lang/rust/"+COMMIT+"/"+path,timeout=30).read()
   assert hashlib.sha256(raw).hexdigest()==sha,path
   target.write_bytes(raw)
  raw=target.read_bytes()
  assert hashlib.sha256(raw).hexdigest()==sha,path
 assert (source_dir/"src__version").read_text().strip()=="1.97.1"
 module=(source_dir/"library__core__src__unicode__unicode_data.rs").read_text()
 # These optimizer assumptions are not callable outside core. Replacing each
 # with an ordinary assertion preserves outputs and verifies its precondition
 # for every enumerated scalar; no data/lookup branch is otherwise changed.
 assert module.count("crate::intrinsics::assume(")==2
 module=module.replace("crate::intrinsics::assume(last_idx < SOR)","assert!(last_idx < SOR)").replace("crate::intrinsics::assume(offset_idx < OFFSETS)","assert!(offset_idx < OFFSETS)")
 (scratch/"unicode_module.rs").write_text(module)
 (scratch/"reference.rs").write_text(RUNNER)
 subprocess.run(["rustc","--edition=2024","-Awarnings",str(scratch/"reference.rs"),"-o",str(scratch/"reference")],check=True)
 table_path=root/"internal/releasemeta/rustanime/unicode_data.go"
 source=table_path.read_text()
 ranges=[]
 for name in ["Alphabetic","Numeric","Uppercase"]:
  body=re.search(r"var source"+name+r"Ranges = \[\]sourceUnicodeRange\{(.*?)\n\}",source,re.S)[1]
  intervals=[tuple(int(v,16) for v in pair) for pair in re.findall(r"\{0x([0-9a-f]+), 0x([0-9a-f]+)\}",body)]
  ranges.append(([lo for lo,hi in intervals],[hi for lo,hi in intervals]))
 maps=[]
 for name in ["Upper","Lower"]:
  body=re.search(r"var source"+name+r"Mappings = map\[rune\]string\{(.*?)\n\}",source,re.S)[1]
  maps.append({int(k,16):v.encode().decode("unicode_escape") for k,v in re.findall(r'0x([0-9a-f]+):\s*"([^"]*)"',body)})
 process=subprocess.Popen([str(scratch/"reference")],stdout=subprocess.PIPE,text=True)
 header=next(process.stdout)
 assert header=="version\t17.0.0\n"
 digest=hashlib.sha256(header.encode())
 count=0;errors=[]
 for row in process.stdout:
  digest.update(row.encode())
  cp,flags,upper,lower=row.rstrip("\n").split("\t");n=int(cp,16)
  expected=0
  for i,(lo,hi) in enumerate(ranges):
   at=bisect.bisect_right(lo,n)-1
   if at>=0 and n<=hi[at]:expected|=1<<i
  calculated=[",".join(format(ord(c),"x") for c in mapping.get(n,chr(n))) for mapping in maps]
  if flags!=str(expected) or [upper,lower]!=calculated:errors.append(dict(codepoint=cp))
  count+=1
 assert process.wait()==0
 report=dict(compiler_commit=COMMIT,compiler_version="1.97.1",unicode_version="17.0.0",source_sha256=SOURCES,scalar_count=count,difference_count=len(errors),first_differences=errors[:20],reference_rows_sha256=digest.hexdigest(),formal_go_sha256=hashlib.sha256(source.encode()).hexdigest())
 print(json.dumps(report,indent=2))
 assert count==1112064 and not errors

if __name__=="__main__":main()
