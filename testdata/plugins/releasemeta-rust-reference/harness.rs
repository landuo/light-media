
fn reference_quote(v: &str) -> String {
 let mut out = String::from("\"");
 for c in v.chars() { match c { '\\' => out.push_str("\\\\"), '"' => out.push_str("\\\""), '\n' => out.push_str("\\n"), '\r' => out.push_str("\\r"), '\t' => out.push_str("\\t"), c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)), _ => out.push(c) } }
 out.push('"'); out
}
fn reference_elements(e: &Elements) -> String {
 let items: Vec<String> = e.reference_values().iter().map(|x|format!("{{\"category\":{},\"value\":{}}}",reference_quote(&format!("{:?}",x.category)),reference_quote(&x.value))).collect();
 format!("[{}]",items.join(","))
}
fn reference_snapshot(name: &str,e: &Elements,tokens: &mut [token::main_token::Token]) -> String {
 let items: Vec<String> = tokens.iter_mut().map(|t| {
  let raw = t.raw_token().to_string(); let inside=t.is_inside_delimiter(); let weak=t.is_weak(); let paren=t.is_paren(); let corner=t.is_japanese_corner();
  let subs: Vec<String> = t.sub_tokens().iter().map(|s| format!("{{\"value\":{},\"found\":{}}}",reference_quote(s.value()),s.is_category(token::subtoken::SubTokenCategory::Found))).collect();
  format!("{{\"raw\":{},\"inside\":{},\"weak\":{},\"paren\":{},\"corner\":{},\"subtokens\":[{}]}}",reference_quote(&raw),inside,weak,paren,corner,subs.join(","))
 }).collect();
 format!("{{\"name\":{},\"elements\":{},\"tokens\":[{}]}}",reference_quote(name),reference_elements(e),items.join(","))
}
fn reference_unhex(v: &str) -> String {
 let bytes: Vec<u8> = (0..v.len()).step_by(2).map(|i|u8::from_str_radix(&v[i..i+2],16).unwrap()).collect(); String::from_utf8(bytes).unwrap()
}
fn main() {
 use std::io::{self,BufRead};
 std::panic::set_hook(Box::new(|_|{}));
 for line in io::stdin().lock().lines() {
  let line=line.unwrap(); let parts: Vec<&str>=line.split('\t').collect();let title=reference_unhex(parts[0]);
  let mut parser=Parser::new(&title);
  if parts.len()>1 { let flags=parts[1].as_bytes();if flags.len()>0{parser=parser.ep_number(flags[0]!=b'0')};if flags.len()>1{parser=parser.ep_title(flags[1]!=b'0')};if flags.len()>2{parser=parser.release_group(flags[2]!=b'0')};if flags.len()>3{parser=parser.file_extension(flags[3]!=b'0')}; }
  if parts.len()>2 && parts[2]!="-" {parser=parser.allowed_delimiters(reference_unhex(parts[2]).chars().collect());}
  if parts.len()>3 && parts[3]!="-" {let items: Vec<String> = parts[3].split(',').map(reference_unhex).collect();parser=parser.ignored_string(items.iter().map(String::as_str).collect());}
  let output = std::panic::catch_unwind(||parser.reference_parse());
  match output {
   Ok(Ok((e,stages))) => { let original=parser.parse().unwrap();assert_eq!(reference_elements(&e),reference_elements(&original));println!("{{\"elements\":{},\"stages\":[{}]}}",reference_elements(&e),stages.join(",")); },
   Ok(Err(e))=>println!("{{\"error\":{}}}",reference_quote(&format!("{}",e))),
   Err(_)=>println!("{{\"error\":\"source parser panic\"}}"),
  }
 }
}
