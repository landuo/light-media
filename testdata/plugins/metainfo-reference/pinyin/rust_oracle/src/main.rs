use std::io::{self, BufRead};
use golia_pinyin::{is_valid_syllable,segment};
include!("../name_wrapper.rs");

fn main() {
    std::panic::set_hook(Box::new(|_| {}));
    for line in io::stdin().lock().lines() {
        let input:serde_json::Value=serde_json::from_str(&line.unwrap()).unwrap();
        let mode=input["mode"].as_str().unwrap();let text=input["text"].as_str().unwrap();
        let result=std::panic::catch_unwind(|| match mode {
            "syllable"=>serde_json::json!(is_valid_syllable(text)),
            "name"=>serde_json::json!(is_pinyin_like(text)),
            "segment"=>serde_json::json!(segment(text).into_iter().map(|s|s.syllables).collect::<Vec<_>>()),
            _=>panic!("unknown mode")
        });
        println!("{}",match result{Ok(value)=>serde_json::json!({"value":value}),Err(_)=>serde_json::json!({"panic":true})});
    }
}
