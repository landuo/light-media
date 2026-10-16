// Offline exhaustive Unicode reference extraction, no runtime dependency.
use std::io::{self, Write};
fn main() {
 let mut out=io::BufWriter::new(io::stdout());
 writeln!(out,"version\t{}.{}.{}",char::UNICODE_VERSION.0,char::UNICODE_VERSION.1,char::UNICODE_VERSION.2).unwrap();
 for n in 0..=0x10ffff {
  if let Some(c)=char::from_u32(n) {
   let flags=(c.is_alphabetic() as u8)|((c.is_numeric() as u8)<<1)|((c.is_uppercase() as u8)<<2);
   let upper: String=c.to_uppercase().collect();
   let lower:String=c.to_lowercase().collect();
   writeln!(out,"{:x}\t{}\t{}\t{}",n,flags,upper.chars().map(|v|format!("{:x}",v as u32)).collect::<Vec<_>>().join(","),lower.chars().map(|v|format!("{:x}",v as u32)).collect::<Vec<_>>().join(",")).unwrap();
  }
 }
}
