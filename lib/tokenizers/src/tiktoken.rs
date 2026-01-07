use anyhow::{Error, Result};
use tiktoken_rs::{cl100k_base, p50k_base, p50k_edit, r50k_base, CoreBPE};

use super::traits::{
    Decoder, Encoder, Encoding, TokenIdType, Tokenizer as TokenizerTrait, TokenizerConfig,
};
use std::{collections::HashSet};


/// Tiktoken tokenizer wrapper for OpenAI GPT models
pub struct TiktokenTokenizer {
    tokenizer: CoreBPE,
    #[allow(dead_code)]
    // model: TiktokenModel,
    vocab_size: usize,
    special_tokens_set: HashSet<String>,
    special_token_ids: HashSet<u32>,
    pub config:  Option<TokenizerConfig>,
    // special_tokens: SpecialTokens,
}

// /// Supported Tiktoken models
// #[derive(Debug, Clone, Copy)]
// pub enum TiktokenModel {
//     /// GPT-4, GPT-3.5-turbo, text-embedding-ada-002
//     Cl100kBase,
//     /// Codex models, text-davinci-002, text-davinci-003
//     P50kBase,
//     /// Use for edit models like text-davinci-edit-001, code-davinci-edit-001
//     P50kEdit,
//     /// GPT-3 models like davinci
//     R50kBase,
// }

impl TiktokenTokenizer {
    /// Create a new Tiktoken tokenizer for the specified model
    pub fn new(model_file_path: &str, config_file_path: &str, pattern: &str) -> Result<TiktokenTokenizer, Box<dyn std::error::Error>> {

        use std::collections::{HashMap, HashSet};
        use tiktoken_rs::{CoreBPE, Rank};
        use base64::{Engine as _, engine::general_purpose};

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
            let raw = parts.next()
                .ok_or_else(|| format!("Invalid model file format at line {}: missing token", line_num + 1))?;
            let token = general_purpose::STANDARD.decode(raw)
                .map_err(|e| format!("Failed to decode base64 at line {}: {}", line_num + 1, e))?;
            let rank: Rank = parts.next()
                .ok_or_else(|| format!("Invalid model file format at line {}: missing rank", line_num + 1))?
                .parse()
                .map_err(|e| format!("Failed to parse rank at line {}: {}", line_num + 1, e))?;
            encoder.insert(token, rank);
        }

        // Parse special tokens from config
        let mut special_tokens: HashMap<String, u32, std::hash::BuildHasherDefault<rustc_hash::FxHasher>> = 
            HashMap::default();
        let mut special_tokens_set = HashSet::new();
        let mut special_token_ids = HashSet::new();
        let config_file = std::fs::File::open(config_file_path)
            .map_err(|e| format!("Failed to open config file: {}", e))?;
        let tokenizer_config: TokenizerConfig = serde_json::from_reader(config_file)
            .map_err(|e| format!("Failed to parse config JSON: {}", e))?;
        {
        
            for (token_id, added_token) in &tokenizer_config.added_tokens_decoder {
                let id: u32 = token_id.parse()
                    .map_err(|e| format!("Failed to parse token ID '{}': {}", token_id, e))?;
                special_tokens.insert(added_token.content.clone(), id);
                special_tokens_set.insert(added_token.content.clone());
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
        Ok(TiktokenTokenizer{
            tokenizer: bpe,
            vocab_size: vocab_size as usize,
            special_tokens_set: special_tokens_set,
            special_token_ids: special_token_ids,
            config: Some(tokenizer_config),
        })
    }

}

impl Encoder for TiktokenTokenizer {
    fn encode(&self, input: &str, _add_special_tokens: bool) -> Result<Encoding> {

        let tokens = self.tokenizer.encode_ordinary(input);
        Ok(Encoding::Tiktoken(tokens))
    }

    fn encode_batch(&self, inputs: &[&str], add_special_tokens: bool) -> Result<Vec<Encoding>> {
        inputs.iter().map(|input| self.encode(input, add_special_tokens)).collect()
    }
}

impl Decoder for TiktokenTokenizer {
    fn decode(&self, token_ids: &[TokenIdType], skip_special_tokens: bool) -> Result<String> {

        // tiktoken-rs 0.7.0 now uses u32 (Rank type)
        // self.tokenizer
        //     .decode(token_ids.to_vec())
        //     .map_err(|e| Error::msg(format!("Decoding failed: {}", e)))


        let tokens_to_decode = if skip_special_tokens {
            token_ids.iter()
                .filter(|id| !self.special_token_ids.contains(id))
                .copied()
                .collect::<Vec<u32>>()
        } else {
            token_ids.to_vec()
        };
        // Handle decode errors by trying to decode progressively fewer tokens
        // until we find a valid UTF-8 sequence, then add replacement characters
        // for the remaining tokens
        match self.tokenizer.decode(tokens_to_decode.clone()) {
            Ok(decoded) => Ok(decoded),
            Err(_) if tokens_to_decode.is_empty() => Ok(String::new()),
            Err(_) => {
                // Try decoding progressively fewer tokens from the end
                let mut valid_prefix_len = tokens_to_decode.len() - 1;
                let mut decoded_prefix = String::new();
                
                while valid_prefix_len > 0 {
                    if let Ok(decoded) = self.tokenizer.decode(tokens_to_decode[..valid_prefix_len].to_vec()) {
                        decoded_prefix = decoded;
                        break;
                    }
                    valid_prefix_len -= 1;
                }
                
                let remaining_tokens = tokens_to_decode.len() - valid_prefix_len;
                if remaining_tokens > 0 {
                    decoded_prefix.push_str(&"\u{FFFD}".repeat(remaining_tokens));
                }
                
                Ok(decoded_prefix)
            }
        }



    }
}

impl TokenizerTrait for TiktokenTokenizer {
    fn vocab_size(&self) -> usize {
        self.vocab_size
    }

    // fn get_special_tokens(&self) -> &SpecialTokens {
    //     &self.special_tokens
    // }


    fn model_max_length(&self) -> u128 {
        self.config.as_ref().map_or(16384, |c| c.model_max_length)
    }

    fn token_to_id(&self, _token: &str) -> Option<TokenIdType> {
        // Tiktoken doesn't provide direct token-to-id mapping
        // We'd need to encode the token and check if it produces a single ID
        None
    }

    fn id_to_token(&self, _id: TokenIdType) -> Option<String> {
        // Tiktoken doesn't provide direct id-to-token mapping
        // We can only decode IDs to text
        None
    }

    fn as_any(&self) -> &dyn std::any::Any {
        self
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Common tiktoken pattern for models like Kimi
    const TIKTOKEN_PATTERN_KIMI: &str = r"[\p{Han}]+|[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]*[\p{Ll}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]+(?i:'s|'t|'re|'ve|'m|'ll|'d)?|[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]+[\p{Ll}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]*(?i:'s|'t|'re|'ve|'m|'ll|'d)?|\p{N}{1,3}| ?[^\s\p{L}\p{N}]+[\r\n]*|\s*[\r\n]+|\s+(?!\S)|\s+";

    /// Create a test tiktoken unified tokenizer
    fn create_test_tiktoken_tokenizer() -> Result<TiktokenTokenizer, Box<dyn std::error::Error>> {
        TiktokenTokenizer::new(
            "test/data/kimi-k2-instruct/tiktoken.model",
            "test/data/kimi-k2-instruct/tokenizer_config.json",
            TIKTOKEN_PATTERN_KIMI
        )
    }
    
    #[test]
    fn test_tiktoken_creation() {
        // let tokenizer = TiktokenTokenizer::new(TiktokenModel::Cl100kBase).unwrap();
        // assert_eq!(tokenizer.vocab_size(), 100256);
        create_test_tiktoken_tokenizer().unwrap();
    }

    #[test]
    fn test_encode_decode() {
        let tokenizer = create_test_tiktoken_tokenizer().unwrap();

        let text = "Hello, world!";
        let encoding = tokenizer.encode(text, false).unwrap();

        let decoded = tokenizer.decode(encoding.token_ids(), false).unwrap();

        assert_eq!(decoded, text);

        let tokens = tokenizer.encode("Hello, world! 你好，世界！", false).unwrap().token_ids().to_vec();

        assert_eq!(tokens, vec![19180, 11, 2695, 0, 220, 33845, 378, 2243, 856]);

    }

    #[test]
    fn test_batch_encode() {
        let tokenizer = create_test_tiktoken_tokenizer().unwrap();

        let texts = vec!["Hello", "World", "Test"];
        let encodings = tokenizer.encode_batch(&texts, false).unwrap();

        assert_eq!(encodings.len(), 3);
    }



    // #[test]
    // fn test_special_tokens() {
    //     let tokenizer = create_test_tiktoken_tokenizer().unwrap();
    //     let special_tokens = tokenizer.get_special_tokens();

    //     assert!(special_tokens.eos_token.is_some());
    //     assert_eq!(special_tokens.eos_token.as_ref().unwrap(), "<|endoftext|>");
    // }

    // #[test]
    // fn test_unrecognized_model_name_returns_error() {
    //     let result = TiktokenTokenizer::from_model_name("distilgpt-2");
    //     assert!(result.is_err());
    //     if let Err(e) = result {
    //         assert!(e.to_string().contains("Unrecognized OpenAI model name"));
    //     }

    //     let result = TiktokenTokenizer::from_model_name("bert-base-uncased");
    //     assert!(result.is_err());
    //     if let Err(e) = result {
    //         assert!(e.to_string().contains("Unrecognized OpenAI model name"));
    //     }

    //     let result = TiktokenTokenizer::from_model_name("llama-7b");
    //     assert!(result.is_err());
    //     if let Err(e) = result {
    //         assert!(e.to_string().contains("Unrecognized OpenAI model name"));
    //     }
    // }

    // #[test]
    // fn test_recognized_model_names() {
    //     assert!(TiktokenTokenizer::from_model_name("gpt-4").is_ok());
    //     assert!(TiktokenTokenizer::from_model_name("gpt-3.5-turbo").is_ok());
    //     assert!(TiktokenTokenizer::from_model_name("text-davinci-003").is_ok());
    //     assert!(TiktokenTokenizer::from_model_name("code-davinci-002").is_ok());
    //     assert!(TiktokenTokenizer::from_model_name("text-curie-001").is_ok());
    //     assert!(TiktokenTokenizer::from_model_name("text-babbage-001").is_ok());
    //     assert!(TiktokenTokenizer::from_model_name("text-ada-001").is_ok());
    // }
}
