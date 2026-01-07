use anyhow::{Error, Result};
use huggingface::HuggingFaceTokenizer;
use llama_tokenizer::LlamaTokenizer;
use std::{path::Path, sync::Arc};
use tiktoken_rs;

mod chat_template;
mod protocol;

mod traits;
use traits::TokenizerConfig;
mod huggingface;
mod llama_tokenizer;
mod tiktoken;

mod ffi;

// Version-specific symbol that will cause link failure if version doesn't match
// Bump minor.patch version every time we bump tokenizers dependency version.
// Can't bump major version because Go doesn't like major version >= 2.
#[no_mangle]
pub extern "C" fn tokenizers_version_1_23_0() {
    // This function exists purely as a link-time version check
}

/// Create a tokenizer from a file path to a tokenizer file.
/// The file extension is used to determine the tokenizer type.
/// Supported file types are:
/// - json: HuggingFace tokenizer
pub fn create_tokenizer_from_file(file_path: &str) -> Result<Arc<dyn traits::Tokenizer>> {
    create_tokenizer_with_chat_template(file_path, None)
}

/// Create a tokenizer from a file path with an optional chat template
pub fn create_tokenizer_with_chat_template(
    file_path: &str,
    chat_template_path: Option<&str>,
) -> Result<Arc<dyn traits::Tokenizer>> {
    let path = Path::new(file_path);

    // Check if file exists
    if !path.exists() {
        return Err(Error::msg(format!("File not found: {}", file_path)));
    }

    // If path is a directory, search for tokenizer files
    if path.is_dir() {
        println!(
            "[tokenizer_rs] Loading tokenizer from directory: {}",
            file_path
        );
        let tokenizer_config = path.join("tokenizer_config.json");
        if tokenizer_config.exists() {
            let content = std::fs::read_to_string(&tokenizer_config)?;
            let config: TokenizerConfig = serde_json::from_str(&content)?;
            let tokenizer_class = config.tokenizer_class;
            // TODO: maybe support other tokenizers
            if tokenizer_class.to_string() == "LlamaTokenizerFast" {
                let tokenizer =
                    LlamaTokenizer::from_dir_with_chat_template(file_path, chat_template_path)?;
                return Ok(Arc::new(tokenizer) as Arc<dyn traits::Tokenizer>);
            }
            return HuggingFaceTokenizer::from_dir_with_chat_template(
                file_path,
                chat_template_path,
            )
            .map(|t| Arc::new(t) as Arc<dyn traits::Tokenizer>);
        } else {
            return HuggingFaceTokenizer::from_dir_with_chat_template(
                file_path,
                chat_template_path,
            )
            .map(|t| Arc::new(t) as Arc<dyn traits::Tokenizer>);
        }
    } else {
        return HuggingFaceTokenizer::from_file_with_chat_template(file_path, chat_template_path)
            .map(|t| Arc::new(t) as Arc<dyn traits::Tokenizer>);
    }
}

/// Creates a CoreBPE encoder from a model file, tokenizer config file, and pattern string.
///
/// # Arguments
/// * `model_file_path` - Path to the .model file containing base64 encoded tokens and ranks
/// * `config_file_path` - Path to the tokenizer_config.json file containing special tokens
/// * `pattern` - Regex pattern string for tokenization
///
/// # Returns
/// A tuple containing:
/// * `CoreBPE` - The tiktoken encoder instance
/// * `u32` - The vocabulary size
/// * `HashSet<String>` - Set of special token strings
/// * `HashSet<u32>` - Set of special token IDs
///
/// # Errors
/// Returns an error if:
/// * File reading fails
/// * Model file format is invalid
/// * Base64 decoding fails
/// * JSON parsing fails
pub fn create_tiktoken_encoder(
    model_file_path: &str,
    config_file_path: &str,
    pattern: &str,
) -> Result<
    (
        tiktoken_rs::CoreBPE,
        u32,
        std::collections::HashSet<String>,
        std::collections::HashSet<u32>,
    ),
    Box<dyn std::error::Error>,
> {
    use base64::{engine::general_purpose, Engine as _};
    use std::collections::{HashMap, HashSet};
    use tiktoken_rs::{CoreBPE, Rank};

    let mut encoder: HashMap<Vec<u8>, Rank, std::hash::BuildHasherDefault<rustc_hash::FxHasher>> =
        HashMap::default();

    // Parse the model file
    let file = std::fs::read_to_string(model_file_path)
        .map_err(|e| format!("Failed to read model file: {}", e))?;

    for (line_num, line) in file.lines().enumerate() {
        if line.trim().is_empty() {
            continue;
        }

        let mut parts = line.split(' ');
        let raw = parts.next().ok_or_else(|| {
            format!(
                "Invalid model file format at line {}: missing token",
                line_num + 1
            )
        })?;
        let token = general_purpose::STANDARD
            .decode(raw)
            .map_err(|e| format!("Failed to decode base64 at line {}: {}", line_num + 1, e))?;
        let rank: Rank = parts
            .next()
            .ok_or_else(|| {
                format!(
                    "Invalid model file format at line {}: missing rank",
                    line_num + 1
                )
            })?
            .parse()
            .map_err(|e| format!("Failed to parse rank at line {}: {}", line_num + 1, e))?;
        encoder.insert(token, rank);
    }

    // Parse special tokens from config
    let mut special_tokens: HashMap<
        String,
        u32,
        std::hash::BuildHasherDefault<rustc_hash::FxHasher>,
    > = HashMap::default();
    let mut special_tokens_set = HashSet::new();
    let mut special_token_ids = HashSet::new();
    {
        let config_file = std::fs::File::open(config_file_path)
            .map_err(|e| format!("Failed to open config file: {}", e))?;
        let tokenizer_config: TokenizerConfig = serde_json::from_reader(config_file)
            .map_err(|e| format!("Failed to parse config JSON: {}", e))?;

        for (token_id, added_token) in tokenizer_config.added_tokens_decoder {
            let id: u32 = token_id
                .parse()
                .map_err(|e| format!("Failed to parse token ID '{}': {}", token_id, e))?;
            special_tokens.insert(added_token.content.clone(), id);
            special_tokens_set.insert(added_token.content);
            special_token_ids.insert(id);
        }
    }

    // Calculate vocab size more robustly
    // The vocab size should be the maximum of:
    // 1. The highest rank in the base vocabulary
    // 2. The highest special token ID
    // Plus 1 to account for 0-based indexing
    let max_rank = encoder.values().copied().max().unwrap_or(0);
    let max_special_token = special_tokens.values().copied().max().unwrap_or(0);
    let vocab_size = std::cmp::max(max_rank, max_special_token) + 1;

    // Fill in missing ranks with reserved special tokens
    // This ensures the encoder has entries for all ranks from 0 to max_rank
    let existing_ranks: HashSet<Rank> = encoder.values().copied().collect();
    let mut reserved_token_count: u32 = 0;
    for rank in 0..max_rank {
        if !existing_ranks.contains(&rank) {
            let reserved_token = format!("<|reserved_special_token_{}|>", reserved_token_count);
            reserved_token_count += 1;
            encoder.insert(reserved_token.into_bytes(), rank);
        }
    }

    let bpe = CoreBPE::new(encoder, special_tokens, pattern)?;
    Ok((bpe, vocab_size, special_tokens_set, special_token_ids))
}

#[cfg(test)]
mod tests {
    use crate::traits::Decoder;

    use super::*;
    use tiktoken::TiktokenTokenizer;

    const TIKTOKEN_PATTERN_KIMI: &str = r"[\p{Han}]+|[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]*[\p{Ll}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]+(?i:'s|'t|'re|'ve|'m|'ll|'d)?|[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]+[\p{Ll}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]*(?i:'s|'t|'re|'ve|'m|'ll|'d)?|\p{N}{1,3}| ?[^\s\p{L}\p{N}]+[\r\n]*|\s*[\r\n]+|\s+(?!\S)|\s+";

    const TIKTOKEN_PATTERN_CL100K_BASE: &str = r"(?i:'s|'t|'re|'ve|'m|'ll|'d)|[^\r\n\p{L}\p{N}]?\p{L}+|\p{N}{1,3}| ?[^\s\p{L}\p{N}]+[\r\n]*|\s*[\r\n]+|\s+(?!\S)|\s+";

    /// Create a test tiktoken unified tokenizer
    fn create_test_tiktoken_tokenizer() -> Result<TiktokenTokenizer, Box<dyn std::error::Error>> {
        TiktokenTokenizer::new(
            "test/data/kimi-k2-instruct/tiktoken.model",
            "test/data/kimi-k2-instruct/tokenizer_config.json",
            TIKTOKEN_PATTERN_KIMI,
        )
    }
    /// Create a test Llama 3 tiktoken unified tokenizer
    fn create_test_llama_tokenizer() -> Result<Arc<dyn traits::Tokenizer>> {
        let tokenizer = TiktokenTokenizer::new(
            "test/data/meta-llama-3-8b-instruct/tiktoken.model",
            "test/data/meta-llama-3-8b-instruct/tokenizer_config.json",
            TIKTOKEN_PATTERN_CL100K_BASE,
        )
        .unwrap();
        Ok(Arc::new(tokenizer))
    }

    #[test]
    fn test_traits_huggingface() -> Result<(), Box<dyn std::error::Error>> {
        // Test with HuggingFace tokenizer
        // let tokenizer = HuggingFaceTokenizer::from_file("test/data/bert-base-uncased.json").map_err(|e| format!("Failed to load tokenizer: {}", e))?;
        let tokenizer =
            create_tokenizer_with_chat_template("test/data/bert-base-uncased.json", None)
                .map_err(|e| format!("Failed to load tokenizer: {}", e))?;
        // let unified = UnifiedTokenizer::HuggingFace(tokenizer);

        let text = "Hello, world!";
        let encodings = tokenizer.encode(text, false).unwrap();
        let ids = encodings.token_ids();
        assert!(!ids.is_empty());

        let decoded = tokenizer.decode(&ids, false).unwrap();
        assert_eq!(decoded.to_lowercase(), text.to_lowercase());

        let vocab_size = tokenizer.vocab_size();
        assert!(vocab_size > 0);

        Ok(())
    }

    #[test]
    fn test_traits_huggingface_with_chat_template() -> Result<(), Box<dyn std::error::Error>> {
        let tokenizer = create_tokenizer_with_chat_template(
            "test/data/Qwen/Qwen3-Coder-480B-A35B-Instruct-FP8",
            Some("test/data/Qwen/Qwen3-Coder-480B-A35B-Instruct-FP8/chat_template.jinja"),
        )
        .map_err(|e| format!("Failed to load tokenizer: {}", e))?;

        let text = "Hello, world!";
        let encodings = tokenizer.encode(text, false).unwrap();
        let ids = encodings.token_ids();
        assert!(!ids.is_empty());

        let decoded = tokenizer.decode(&ids, false).unwrap();
        assert_eq!(decoded.to_lowercase(), text.to_lowercase());

        let vocab_size = tokenizer.vocab_size();
        assert!(vocab_size > 0);

        let messages = r#"
        [
            {"role": "system", "content": "Youre a helpful assistant! Answer the users question best you can."},
            {"role": "user", "content": "What is the weather like in Brooklyn, New York?"}
        ]
        "#;

        // let messages: Vec<serde_json::Value> = serde_json::from_str(messages).unwrap();

        let hf_tokenizer = tokenizer
            .as_any()
            .downcast_ref::<HuggingFaceTokenizer>()
            .or_else(|| {
                tokenizer
                    .as_any()
                    .downcast_ref::<LlamaTokenizer>()
                    .map(|llama| llama.inner())
            })
            .ok_or("Failed to downcast tokenizer")?;

        let tools_string = r#"[{"type": "function","function": {"name": "get_current_weather","description": "Get the current weather","parameters": {"type": "object","properties": {"location": {"type": "string","description": "The city and state, e.g. San Francisco, CA"},"format": {"type": "string","enum": ["celsius", "fahrenheit"],"description": "The temperature unit to use. Infer this from the users location."}},"required": ["location", "format"]}}}]"#;

        let result = hf_tokenizer
            .apply_chat_template(messages, tools_string, "")
            .unwrap();

        assert_eq!(
            result,
            r#"<|im_start|>system
Youre a helpful assistant! Answer the users question best you can.

# Tools

You have access to the following functions:

<tools>
<function>
<name>get_current_weather</name>
<description>Get the current weather</description>
<parameters>
<parameter>
<name>format</name>
<type>string</type>
<description>The temperature unit to use. Infer this from the users location.</description>
<enum>["celsius","fahrenheit"]</enum>

</parameter>
<parameter>
<name>location</name>
<type>string</type>
<description>The city and state, e.g. San Francisco, CA</description>

</parameter>
        
<required>["location","format"]</required>

</parameters>

</function>
</tools>

If you choose to call a function ONLY reply in the following format with NO suffix:

<tool_call>
<function=example_function_name>
<parameter=example_parameter_1>
value_1
</parameter>
<parameter=example_parameter_2>
This is the value for the second parameter
that can span
multiple lines
</parameter>
</function>
</tool_call>

<IMPORTANT>
Reminder:
- Function calls MUST follow the specified format: an inner <function=...></function> block must be nested within <tool_call></tool_call> XML tags
- Required parameters MUST be specified
- You may provide optional reasoning for your function call in natural language BEFORE the function call, but NOT after
- If there is no function call available, answer the question like normal with your current knowledge and do not tell the user about function calls
</IMPORTANT><|im_end|>
<|im_start|>user
What is the weather like in Brooklyn, New York?<|im_end|>
<|im_start|>assistant
"#.to_string()
        );

        Ok(())
    }

    #[test]
    fn test_traits_llama() -> Result<(), Box<dyn std::error::Error>> {
        let tokenizer =
            create_tokenizer_with_chat_template("test/data/deepseek-ai/DeepSeek-R1", None)
                .map_err(|e| format!("Failed to load tokenizer: {}", e))?;
        // let unified = UnifiedTokenizer::HuggingFace(tokenizer);

        let text = "Hello, world!";
        let encodings = tokenizer.encode(text, true).unwrap();
        let ids = encodings.token_ids();
        assert_eq!(ids, vec![151646, 9707, 11, 1879, 0]);

        let decoded = tokenizer.decode(&ids, false).unwrap();
        assert_eq!(decoded, "<｜begin▁of▁sentence｜>Hello, world!");

        let vocab_size = tokenizer.vocab_size();
        assert!(vocab_size > 0);

        Ok(())
    }

    #[test]
    fn test_partial_decode() -> Result<(), Box<dyn std::error::Error>> {
        // Test that tiktoken can handle incomplete UTF-8 by returning replacement characters
        let unified = create_test_llama_tokenizer()?;

        // Test cases for partial and complete UTF-8 sequences
        let test_cases = vec![
            (
                vec![15722],
                "\u{FFFD}",
                "Partial decode should return replacement character",
            ),
            (
                vec![15722, 103],
                "歪",
                "Complete UTF-8 should decode correctly",
            ),
            (
                vec![15722, 103, 15722],
                "歪\u{FFFD}",
                "Partial decode should return replacement character",
            ),
            (
                vec![15722, 103, 15722, 103],
                "歪歪",
                "Complete UTF-8 should decode correctly",
            ),
            (
                vec![15722, 103, 15722, 15722],
                "歪\u{FFFD}\u{FFFD}",
                "Multiple non-decoded tokens should return replacement characters",
            ),
        ];

        for (ids, expected, description) in test_cases {
            let decoded = unified.decode(&ids, false)?;
            assert_eq!(
                decoded, expected,
                "{}: Decoded text should match expected",
                description
            );
        }

        Ok(())
    }

    #[test]
    fn test_partial_decode_multiple_tokens() -> Result<(), Box<dyn std::error::Error>> {
        // Test the new approach with various scenarios
        let unified = create_test_llama_tokenizer()?;

        // Encode "Hello 歪 world" and test various partial decodes
        let full_text = "Hello 歪 world";
        let ids = unified.encode(full_text, false)?;
        let ids = ids.token_ids();

        // Full decode should work
        let decoded = unified.decode(&ids, false)?;
        assert_eq!(decoded, full_text, "Full decode should work correctly");

        // Test decoding with the last token removed (might break UTF-8)
        if ids.len() > 1 {
            let partial_ids = &ids[..ids.len() - 1];
            let decoded_partial = unified.decode(partial_ids, false)?;
            // This should either decode correctly or have replacement chars at the end
            assert!(
                decoded_partial.contains("Hello"),
                "Partial decode should preserve valid prefix"
            );
        }

        // Test with just the incomplete UTF-8 tokens
        let incomplete_utf8_ids = vec![15722]; // Just the first part of "歪"
        let decoded_incomplete = unified.decode(&incomplete_utf8_ids, false)?;
        assert_eq!(
            decoded_incomplete, "\u{FFFD}",
            "Single incomplete token should be replacement char"
        );

        // Test with multiple tokens where the last few form incomplete UTF-8
        let text_with_emoji = "Test 👋";
        let emoji_ids = unified.encode(text_with_emoji, false)?;
        let emoji_ids = emoji_ids.token_ids();
        if emoji_ids.len() > 2 {
            // Remove last token to potentially break the emoji
            let partial_emoji_ids = &emoji_ids[..emoji_ids.len() - 1];
            let decoded_partial_emoji = unified.decode(partial_emoji_ids, false)?;
            assert!(
                decoded_partial_emoji.starts_with("Test"),
                "Should preserve 'Test' prefix"
            );
            // The emoji part should either decode correctly or be replaced
        }

        Ok(())
    }

    #[test]
    fn test_unified_llama() -> Result<(), Box<dyn std::error::Error>> {
        // Test Llama 3 tiktoken functionality
        let unified = create_test_llama_tokenizer()?;

        // Test basic English encoding/decoding
        let text_en = "Hello, world!";
        let ids_en = unified.encode(text_en, false)?;
        let ids_en = ids_en.token_ids();
        assert!(!ids_en.is_empty());
        let decoded_en = unified.decode(&ids_en, false)?;
        assert_eq!(decoded_en, text_en);

        // Test contractions and punctuation
        let text_contractions = "I'm here, you're there. It's great!";
        let ids_contractions = unified.encode(text_contractions, false)?;
        let ids_contractions = ids_contractions.token_ids();
        assert!(!ids_contractions.is_empty());
        let decoded_contractions = unified.decode(&ids_contractions, false)?;
        assert_eq!(decoded_contractions, text_contractions);

        // Test multilingual support with various languages
        let text_multilingual = "Hello world! 你好世界！ مرحبا بالعالم Bonjour le monde! 🌍";
        let ids_multilingual = unified.encode(text_multilingual, false)?;
        let ids_multilingual = ids_multilingual.token_ids();
        assert!(!ids_multilingual.is_empty());
        let decoded_multilingual = unified.decode(&ids_multilingual, false)?;
        assert_eq!(decoded_multilingual, text_multilingual);

        // Test numbers and mixed content
        let text_mixed = "Test123: The price is $99.99 USD (≈€92.50 EUR)";
        let ids_mixed = unified.encode(text_mixed, false)?;
        let ids_mixed = ids_mixed.token_ids();
        assert!(!ids_mixed.is_empty());
        let decoded_mixed = unified.decode(&ids_mixed, false)?;
        assert_eq!(decoded_mixed, text_mixed);

        // Test vocab size (Llama 3 has 128,256 base tokens)
        let vocab_size = unified.vocab_size();
        assert!(
            vocab_size >= 128256,
            "Llama 3 vocab size should be at least 128256, got {}",
            vocab_size
        );

        Ok(())
    }

    #[test]
    fn test_llama_special_tokens() -> Result<(), Box<dyn std::error::Error>> {
        // Test special token parsing and handling for Llama 3
        let (_bpe, _vocab_size, special_tokens, special_token_ids) = create_tiktoken_encoder(
            "test/data/meta-llama-3-8b-instruct/tiktoken.model",
            "test/data/meta-llama-3-8b-instruct/tokenizer_config.json",
            TIKTOKEN_PATTERN_CL100K_BASE,
        )?;

        // Verify special tokens were parsed
        assert!(
            !special_tokens.is_empty(),
            "Special tokens should have been parsed from config"
        );
        assert!(
            !special_token_ids.is_empty(),
            "Special token IDs should have been parsed"
        );

        // Check for expected Llama special tokens
        let has_begin_text = special_tokens.iter().any(|t| t == "<|begin_of_text|>");
        let has_end_text = special_tokens.iter().any(|t| t == "<|end_of_text|>");
        let has_start_header = special_tokens.iter().any(|t| t == "<|start_header_id|>");
        let has_end_header = special_tokens.iter().any(|t| t == "<|end_header_id|>");
        let has_eot = special_tokens.iter().any(|t| t == "<|eot_id|>");

        assert!(
            has_begin_text,
            "Expected to find <|begin_of_text|> special token"
        );
        assert!(
            has_end_text,
            "Expected to find <|end_of_text|> special token"
        );
        assert!(
            has_start_header,
            "Expected to find <|start_header_id|> special token"
        );
        assert!(
            has_end_header,
            "Expected to find <|end_header_id|> special token"
        );
        assert!(has_eot, "Expected to find <|eot_id|> special token");

        // Verify special token IDs are in the expected range (128000-128255 for Llama 3)
        assert!(
            special_token_ids.contains(&128000),
            "Should contain begin_of_text token ID"
        );
        assert!(
            special_token_ids.contains(&128001),
            "Should contain end_of_text token ID"
        );
        assert!(
            special_token_ids.contains(&128006),
            "Should contain start_header_id token ID"
        );
        assert!(
            special_token_ids.contains(&128007),
            "Should contain end_header_id token ID"
        );
        assert!(
            special_token_ids.contains(&128009),
            "Should contain eot_id token ID"
        );

        Ok(())
    }

    #[test]
    fn test_llama_skip_special_tokens_decoding() -> Result<(), Box<dyn std::error::Error>> {
        // Test that skip_special_tokens correctly filters out special token IDs during decoding
        let unified = create_test_llama_tokenizer()?;

        // Create a sequence with special token IDs
        // Example: <|begin_of_text|>Hello, world!<|end_of_text|>
        let ids_with_special = vec![128000, 9906, 11, 1917, 0, 128001]; // [begin_of_text] Hello, world! [end_of_text]

        // Decode without skipping special tokens
        let decoded_with_special = unified.decode(&ids_with_special, false)?;
        assert!(
            decoded_with_special.contains("<|begin_of_text|>")
                || decoded_with_special.contains("<|end_of_text|>"),
            "Decoded text should contain special tokens when skip_special_tokens is false"
        );

        // Decode with skipping special tokens
        let decoded_skip_special = unified.decode(&ids_with_special, true)?;
        assert!(
            !decoded_skip_special.contains("<|begin_of_text|>"),
            "Decoded text should NOT contain <|begin_of_text|> when skip_special_tokens is true"
        );
        assert!(
            !decoded_skip_special.contains("<|end_of_text|>"),
            "Decoded text should NOT contain <|end_of_text|> when skip_special_tokens is true"
        );
        assert_eq!(
            decoded_skip_special, "Hello, world!",
            "Decoded text should only contain the regular tokens"
        );

        Ok(())
    }

    #[test]
    fn test_llama_chat_template_tokens() -> Result<(), Box<dyn std::error::Error>> {
        // Test encoding/decoding of chat template patterns
        let unified = create_test_llama_tokenizer()?;

        // Test typical chat template markers
        let chat_markers = vec!["<|start_header_id|>", "<|end_header_id|>", "<|eot_id|>"];

        for marker in chat_markers {
            let ids = unified.encode(marker, true)?;
            let ids = ids.token_ids();
            assert!(
                !ids.is_empty(),
                "Chat marker '{}' should be encoded",
                marker
            );

            let decoded = unified.decode(&ids, false)?;
            assert_eq!(decoded, marker, "Chat marker should decode correctly");
        }

        // Test a full chat template sequence
        let chat_sequence =
            "<|begin_of_text|><|start_header_id|>user<|end_header_id|>\n\nHello!<|eot_id|>";
        let ids = unified.encode(chat_sequence, true)?;
        let ids = ids.token_ids();
        assert!(!ids.is_empty(), "Chat sequence should be encoded");

        let decoded = unified.decode(&ids, false)?;
        assert_eq!(
            decoded, chat_sequence,
            "Chat sequence should decode correctly"
        );

        Ok(())
    }

    #[test]
    fn test_tiktoken_special_tokens() -> Result<(), Box<dyn std::error::Error>> {
        // Test special token parsing and handling
        let (_bpe, _vocab_size, special_tokens, special_token_ids) = create_tiktoken_encoder(
            "test/data/kimi-k2-instruct/tiktoken.model",
            "test/data/kimi-k2-instruct/tokenizer_config.json",
            TIKTOKEN_PATTERN_KIMI,
        )?;

        // Verify special tokens were parsed
        assert!(
            !special_tokens.is_empty(),
            "Special tokens should have been parsed from config"
        );
        assert!(
            !special_token_ids.is_empty(),
            "Special token IDs should have been parsed"
        );

        // Check for expected special tokens
        let has_bos = special_tokens.iter().any(|t| t == "[BOS]");
        let has_eos = special_tokens.iter().any(|t| t == "[EOS]");
        let has_im_end = special_tokens.iter().any(|t| t == "<|im_end|>");
        assert!(
            has_bos || has_eos || has_im_end,
            "Expected to find at least one special token"
        );

        // Verify special token IDs are in the expected range
        assert!(
            special_token_ids.contains(&163584),
            "Should contain BOS token ID"
        );
        assert!(
            special_token_ids.contains(&163585),
            "Should contain EOS token ID"
        );

        Ok(())
    }

    #[test]
    fn test_tiktoken_skip_special_tokens_decoding() -> Result<(), Box<dyn std::error::Error>> {
        // Test that skip_special_tokens correctly filters out special token IDs during decoding
        let unified = create_test_tiktoken_tokenizer()?;

        // Create a sequence with special token IDs
        let ids_with_special = vec![163584, 19180, 11, 2695, 0, 163585]; // [BOS] Hello, world! [EOS]

        // Decode without skipping special tokens
        let decoded_with_special = unified.decode(&ids_with_special, false)?;
        assert!(
            decoded_with_special.contains("[BOS]") || decoded_with_special.contains("[EOS]"),
            "Decoded text should contain special tokens when skip_special_tokens is false"
        );

        // Decode with skipping special tokens
        let decoded_skip_special = unified.decode(&ids_with_special, true)?;
        assert!(
            !decoded_skip_special.contains("[BOS]"),
            "Decoded text should NOT contain [BOS] when skip_special_tokens is true"
        );
        assert!(
            !decoded_skip_special.contains("[EOS]"),
            "Decoded text should NOT contain [EOS] when skip_special_tokens is true"
        );
        assert_eq!(
            decoded_skip_special, "Hello, world!",
            "Decoded text should only contain the regular tokens"
        );

        Ok(())
    }

    #[test]
    fn test_qwen3_8b_tools() -> Result<(), Box<dyn std::error::Error>> {
        let tokenizer = create_tokenizer_with_chat_template("test/data/Qwen/Qwen3-8B", None)
            .map_err(|e| format!("Failed to load tokenizer: {}", e))?;

        let text = "Hello, world!";
        let encodings = tokenizer.encode(text, false).unwrap();
        let ids = encodings.token_ids();
        assert!(!ids.is_empty());

        let decoded = tokenizer.decode(&ids, false).unwrap();
        assert_eq!(decoded.to_lowercase(), text.to_lowercase());

        let vocab_size = tokenizer.vocab_size();
        assert!(vocab_size > 0);

        let model_max_len = tokenizer.model_max_length();
        assert!(model_max_len == 131072);

        let messages = r#"
        [
            {
            "role": "system",
            "content": "RemoteProcedureCall(GetFunctionIndex) will get a list of all available functions in your host program"
            },
            {
            "role": "user",
            "content": "Test the RPC functionality by calling TestFunction(1, 2, 3, 4, 5, 6, 7, 8)"
            }
        ]
        "#;

        // let messages: Vec<serde_json::Value> = serde_json::from_str(messages).unwrap();

        let hf_tokenizer = tokenizer
            .as_any()
            .downcast_ref::<HuggingFaceTokenizer>()
            .or_else(|| {
                tokenizer
                    .as_any()
                    .downcast_ref::<LlamaTokenizer>()
                    .map(|llama| llama.inner())
            })
            .ok_or("Failed to downcast tokenizer")?;

        let tools_string = r#"[
    {
      "type": "function",
      "function": {
        "name": "RemoteProcedureCall",
        "description": "Invoke a function (by name) in the program hosting the large language model",
        "parameters": {
          "type": "object",
          "properties": {
            "functionname": {
              "type": "string",
              "description": "Name of function to invoke"
            },
            "parameter1": {
              "type": "string",
              "description": "parameter 1"
            },
            "parameter2": {
              "type": "string",
              "description": "parameter 2"
            },
            "parameter3": {
              "type": "string",
              "description": "parameter 3"
            },
            "parameter4": {
              "type": "string",
              "description": "parameter 4"
            },
            "parameter5": {
              "type": "string",
              "description": "parameter 5"
            },
            "parameter6": {
              "type": "string",
              "description": "parameter 6"
            },
            "parameter7": {
              "type": "string",
              "description": "parameter 7"
            },
            "parameter8": {
              "type": "string",
              "description": "parameter 8"
            }
          },
          "required": [
            "functionname"
          ]
        }
      }
    }
  ]"#;

        let result = hf_tokenizer
            .apply_chat_template(&messages, tools_string, "")
            .unwrap();
        // println!("Result:\n{}", result);
        // assert_eq!(
        //     result,
        //     r#"<|im_start|>system"#);

        Ok(())
    }
}
