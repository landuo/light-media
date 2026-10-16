fn is_pinyin_like(name: &str) -> bool {
    let words = name
        .split_whitespace()
        .filter_map(|word| {
            let cleaned = word
                .trim_matches(|ch: char| !ch.is_ascii_alphabetic())
                .to_ascii_lowercase();
            (!cleaned.is_empty()).then_some(cleaned)
        })
        .collect::<Vec<_>>();
    !words.is_empty() && words.iter().all(|word| is_ascii_pinyin_word(word))
}

fn is_ascii_pinyin_word(word: &str) -> bool {
    word.bytes().all(|byte| byte.is_ascii_alphabetic())
        && (is_valid_syllable(word)
            || segment(word)
                .first()
                .is_some_and(|item| !item.syllables.is_empty()))
}
