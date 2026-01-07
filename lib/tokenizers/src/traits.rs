use anyhow::Result;
use std::collections::hash_map::DefaultHasher;
use std::collections::HashMap;
use std::hash::{Hash, Hasher};

/// Type alias for token IDs
pub type TokenIdType = u32;
use serde::{Deserialize, Serialize};
use tokenizers::AddedToken;

// #[derive(Debug, Deserialize, Serialize, Clone, PartialEq)]
// pub struct AddedToken {
//     #[serde(default)]
//     #[serde(skip_serializing_if = "Option::is_none")]
//     pub id: Option<u32>,
//     #[serde(default)]
//     #[serde(skip_serializing_if = "Option::is_none")]
//     __type: Option<String>,
//     pub content: String,
//     pub single_word: bool,
//     pub lstrip: bool,
//     pub rstrip: bool,
//     pub normalized: bool,
//     pub special: Option<bool>,
// }

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(untagged)]
pub enum TokenizerConfigToken {
    String(String),
    Object { content: String },
    AddedToken(AddedToken),
}

impl TokenizerConfigToken {
    pub fn as_str(&self) -> &str {
        match self {
            TokenizerConfigToken::String(s) => s,
            TokenizerConfigToken::Object { content } => content,
            TokenizerConfigToken::AddedToken(at) => &at.content,
        }
    }

    // pub fn as_token_id(&self) -> u32 {
    //     match self {
    //         TokenizerConfigToken::String(_) => panic!("Cannot get token ID from String variant"),
    //         TokenizerConfigToken::Object { .. } => panic!("Cannot get token ID from Object variant"),
    //         TokenizerConfigToken::AddedToken(at) => at.id.expect("AddedToken must have an ID"),
    //     }
    // }
}

// #[derive(Debug, Clone)]
// pub struct SpecialTokens {
//     pub bos_token: Option<TokenizerConfigToken>,
//     pub eos_token: Option<TokenizerConfigToken>,
//     pub unk_token: Option<TokenizerConfigToken>,
//     pub sep_token: Option<TokenizerConfigToken>,
//     pub pad_token: Option<TokenizerConfigToken>,
//     pub cls_token: Option<TokenizerConfigToken>,
//     pub mask_token: Option<TokenizerConfigToken>,
//     pub additional_special_tokens: Vec<TokenizerConfigToken>,
// }

// Structures for deserializing tokenizer_config.json
#[derive(Debug, Deserialize, Serialize, Default)]
#[serde(default)]
pub struct TokenizerConfig {
    pub added_tokens_decoder: HashMap<String, AddedToken>,
    pub additional_special_tokens: Option<Vec<String>>,
    pub bos_token: Option<TokenizerConfigToken>,
    pub eos_token: Option<TokenizerConfigToken>,
    pub pad_token: Option<TokenizerConfigToken>,
    pub sep_token: Option<TokenizerConfigToken>,
    pub unk_token: Option<TokenizerConfigToken>,
    pub cls_token: Option<TokenizerConfigToken>,
    pub mask_token: Option<TokenizerConfigToken>,
    pub model_max_length: u128, // Changed from u32 to handle very large values
    pub tokenizer_class: String,
    pub chat_template: Option<String>,

    // Added fields for flexibility
    pub add_bos_token: Option<bool>,
    pub add_eos_token: Option<bool>,
    pub add_prefix_space: Option<bool>,
    // pub legacy: Option<bool>,
}

/// Core encoding trait - separate from decoding for modularity
pub trait Encoder: Send + Sync {
    fn encode(&self, input: &str, add_special_tokens: bool) -> Result<Encoding>;
    fn encode_batch(&self, inputs: &[&str], add_special_tokens: bool) -> Result<Vec<Encoding>>;
}

/// Core decoding trait - can be implemented independently
pub trait Decoder: Send + Sync {
    fn decode(&self, token_ids: &[TokenIdType], skip_special_tokens: bool) -> Result<String>;
}

/// Combined tokenizer trait
pub trait Tokenizer: Encoder + Decoder {
    fn vocab_size(&self) -> usize;
    // fn get_special_tokens(&self) -> &SpecialTokens;
    fn token_to_id(&self, token: &str) -> Option<TokenIdType>;
    fn id_to_token(&self, id: TokenIdType) -> Option<String>;

    fn model_max_length(&self) -> u128;
    /// Enable downcasting to concrete types
    fn as_any(&self) -> &dyn std::any::Any;
}

/// Contains the results of tokenizing text: token IDs, string tokens, and their spans
#[derive(Debug, Clone)]
pub enum Encoding {
    /// Hugging Face
    Hf(Box<tokenizers::tokenizer::Encoding>),
    /// Sentence Piece
    Sp(Vec<TokenIdType>),
    /// Tiktoken (for GPT models) - now uses u32 in tiktoken-rs 0.7.0
    Tiktoken(Vec<TokenIdType>),
}

impl Encoding {
    /// Returns a reference to token IDs - zero-copy operation
    pub fn token_ids(&self) -> &[TokenIdType] {
        match self {
            Encoding::Hf(inner) => inner.get_ids(),
            Encoding::Sp(inner) => inner,
            Encoding::Tiktoken(inner) => inner,
        }
    }

    /// Get a hash of the token IDs for caching purposes
    pub fn get_hash(&self) -> u64 {
        let mut hasher = DefaultHasher::new();
        self.hash(&mut hasher);
        hasher.finish()
    }
}

/// Hash implementation for Encoding
impl Hash for Encoding {
    fn hash<H: Hasher>(&self, state: &mut H) {
        match self {
            Encoding::Hf(inner) => inner.get_ids().hash(state),
            Encoding::Sp(inner) => inner.hash(state),
            Encoding::Tiktoken(inner) => inner.hash(state),
        }
    }
}
