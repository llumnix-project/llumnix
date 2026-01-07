// This file is modified from https://github.com/sgl-project/sglang/blob/main/sgl-router/src/tokenizer/huggingface.rs

use std::collections::HashMap;

use anyhow::{Error, Result};
use tokenizers::tokenizer::Tokenizer as HfTokenizer;

use crate::chat_template;

use super::chat_template::ChatTemplate;

use super::traits::{
    Decoder, Encoder, Encoding, TokenIdType, Tokenizer as TokenizerTrait, TokenizerConfig,
    TokenizerConfigToken,
};

/// HuggingFace tokenizer wrapper
pub struct HuggingFaceTokenizer {
    pub tokenizer: HfTokenizer,
    // special_tokens: SpecialTokens,
    pub config: Option<TokenizerConfig>,
    vocab: HashMap<String, TokenIdType>,
    reverse_vocab: HashMap<TokenIdType, String>,
    chat_template: Option<ChatTemplate>,
}

impl HuggingFaceTokenizer {
    fn build_template_kwargs(config: &TokenizerConfig) -> HashMap<String, serde_json::Value> {
        let mut template_kwargs: HashMap<String, serde_json::Value> = HashMap::new();

        template_kwargs.insert(
            "bos_token".into(),
            serde_json::Value::String(
                config
                    .bos_token
                    .as_ref()
                    .map_or("".to_string(), |t| t.as_str().to_string()),
            ),
        );

        template_kwargs.insert(
            "eos_token".into(),
            serde_json::Value::String(
                config
                    .eos_token
                    .as_ref()
                    .map_or("".to_string(), |t| t.as_str().to_string()),
            ),
        );

        template_kwargs
    }

    pub fn from_dir(model_path_str: &str) -> Result<Self> {
        let model_path = std::path::Path::new(model_path_str);

        let tokenizer_path = model_path.join("tokenizer.json");

        let tokenizer = HfTokenizer::from_file(tokenizer_path)
            .map_err(|e| Error::msg(format!("Failed to load pretrained tokenizer: {}", e)))?;

        let special_tokens = Self::extract_special_tokens(&tokenizer);

        // Build vocab mappings
        let vocab = tokenizer.get_vocab(false);
        let reverse_vocab: HashMap<TokenIdType, String> = vocab
            .iter()
            .map(|(token, &id)| (id, token.clone()))
            .collect();

        // tokenizer_config
        let config_path = model_path.join("tokenizer_config.json");

        if !config_path.exists() {
            return Ok(HuggingFaceTokenizer {
                tokenizer,
                // special_tokens,
                config: None,
                vocab,
                reverse_vocab,
                chat_template: None,
            });
        }

        let content = std::fs::read_to_string(&config_path)
            .map_err(|e| Error::msg(format!("Failed to read tokenizer_config.json: {}", e)))?;

        let config = serde_json::from_str::<TokenizerConfig>(&content)
            .map_err(|e| Error::msg(format!("Failed to parse tokenizer_config.json: {}", e)))?;

        let template_kwargs = Self::build_template_kwargs(&config);

        let chat_template = ChatTemplate::new(
            config.chat_template.clone().unwrap_or_default(),
            Some(template_kwargs),
        );

        Ok(HuggingFaceTokenizer {
            tokenizer,
            config: Some(config),
            vocab,
            reverse_vocab,
            chat_template: Some(chat_template),
        })
    }

    pub fn from_dir_with_chat_template(
        model_path_str: &str,
        chat_template_path: Option<&str>,
    ) -> Result<Self> {
        // default to from_dir if no chat template is provided
        if !chat_template_path.is_some() || chat_template_path.unwrap().is_empty() {
            return Self::from_dir(model_path_str);
        }

        let model_path = std::path::Path::new(model_path_str);

        let tokenizer_path = model_path.join("tokenizer.json");

        let tokenizer = HfTokenizer::from_file(tokenizer_path)
            .map_err(|e| Error::msg(format!("Failed to load pretrained tokenizer: {}", e)))?;

        let special_tokens = Self::extract_special_tokens(&tokenizer);

        // Build vocab mappings
        let vocab = tokenizer.get_vocab(false);
        let reverse_vocab: HashMap<TokenIdType, String> = vocab
            .iter()
            .map(|(token, &id)| (id, token.clone()))
            .collect();

        // tokenizer_config
        let config_path = model_path.join("tokenizer_config.json");

        if !config_path.exists() && chat_template_path.is_none() {
            return Ok(HuggingFaceTokenizer {
                tokenizer,
                // special_tokens,
                config: None,
                vocab,
                reverse_vocab,
                chat_template: None,
            });
        }

        let content: String = std::fs::read_to_string(&config_path)
            .map_err(|e| Error::msg(format!("Failed to read tokenizer_config.json: {}", e)))?;

        let config = serde_json::from_str::<TokenizerConfig>(&content)
            .map_err(|e| Error::msg(format!("Failed to parse tokenizer_config.json: {}", e)))?;

        let template_kwargs = Self::build_template_kwargs(&config);

        let chat_template_str = Self::load_chat_template_from_file(chat_template_path.unwrap())?;

        let chat_template =
            ChatTemplate::new(chat_template_str.unwrap_or_default(), Some(template_kwargs));

        Ok(HuggingFaceTokenizer {
            tokenizer,
            config: Some(config),
            vocab,
            reverse_vocab,
            chat_template: Some(chat_template),
            // content_format: ChatTemplateContentFormat::String, // Default
        })
    }

    /// Create a tokenizer from a HuggingFace tokenizer JSON file
    pub fn from_file(file_path: &str) -> Result<Self> {
        // Try to auto-discover chat template if not explicitly provided
        let path = std::path::Path::new(file_path);
        let chat_template_path = path
            .parent()
            .and_then(chat_template::discover_chat_template_in_dir);
        Self::from_file_with_chat_template(file_path, chat_template_path.as_deref())
    }

    /// Create a tokenizer from a HuggingFace tokenizer JSON file with an optional chat template
    pub fn from_file_with_chat_template(
        file_path: &str,
        chat_template_path: Option<&str>,
    ) -> Result<Self> {
        let tokenizer = HfTokenizer::from_file(file_path)
            .map_err(|e| Error::msg(format!("Failed to load tokenizer: {}", e)))?;

        // Extract special tokens
        let config = Self::extract_special_tokens(&tokenizer);

        // Build vocab mappings
        let vocab = tokenizer.get_vocab(false);
        let reverse_vocab: HashMap<TokenIdType, String> = vocab
            .iter()
            .map(|(token, &id)| (id, token.clone()))
            .collect();

        if chat_template_path.is_none() || chat_template_path.unwrap().is_empty() {
            return Ok(HuggingFaceTokenizer {
                tokenizer,
                config: Some(config),
                vocab,
                reverse_vocab,
                chat_template: None,
            });
        }

        // Load chat template
        let chat_template_str = if let Some(template_path) = chat_template_path {
            // Load from specified .jinja file
            println!("[tokenizer_rs] load chat template from specified .jinja file");
            Self::load_chat_template_from_file(template_path)?
        } else {
            // Try to load from tokenizer_config.json
            println!("[tokenizer_rs] load chat template from tokenizer_config.json");
            Self::load_chat_template(file_path)
        };

        let template_kwargs = Self::build_template_kwargs(&config);

        let chat_template =
            ChatTemplate::new(chat_template_str.unwrap_or_default(), Some(template_kwargs));

        Ok(HuggingFaceTokenizer {
            tokenizer,
            config: Some(config),
            vocab,
            reverse_vocab,
            chat_template: Some(chat_template),
            // content_format,
        })
    }

    /// Create from an existing HuggingFace tokenizer
    pub fn from_tokenizer(tokenizer: HfTokenizer) -> Self {
        let config = Self::extract_special_tokens(&tokenizer);
        let vocab = tokenizer.get_vocab(false);
        let reverse_vocab: HashMap<TokenIdType, String> = vocab
            .iter()
            .map(|(token, &id)| (id, token.clone()))
            .collect();

        HuggingFaceTokenizer {
            tokenizer,
            config: Some(config),
            vocab,
            reverse_vocab,
            chat_template: None,
            // content_format: ChatTemplateContentFormat::String, // Default
        }
    }

    /// Extract special tokens from the tokenizer
    fn extract_special_tokens(tokenizer: &HfTokenizer) -> TokenizerConfig {
        // Try to get special tokens from the tokenizer
        // This is a simplified version - actual implementation would need to handle various formats
        let vocab = tokenizer.get_vocab(true);

        let find_token = |patterns: &[&str]| -> Option<TokenizerConfigToken> {
            for pattern in patterns {
                if vocab.contains_key(*pattern) {
                    return Some(TokenizerConfigToken::String(pattern.to_string()));
                }
            }
            None
        };

        TokenizerConfig {
            bos_token: find_token(&["<s>", "<|startoftext|>", "<BOS>", "[CLS]"]),
            eos_token: find_token(&["</s>", "<|endoftext|>", "<EOS>", "[SEP]"]),
            unk_token: find_token(&["<unk>", "<UNK>", "[UNK]"]),
            sep_token: find_token(&["[SEP]", "<sep>", "<SEP>"]),
            pad_token: find_token(&["<pad>", "<PAD>", "[PAD]"]),
            cls_token: find_token(&["[CLS]", "<cls>", "<CLS>"]),
            mask_token: find_token(&["[MASK]", "<mask>", "<MASK>"]),
            // additional_special_tokens: vec![],
            ..Default::default()
        }
    }

    /// Try to load chat template from tokenizer_config.json
    fn load_chat_template(template_path: &str) -> Option<String> {
        // Try to find tokenizer_config.json in the same directory
        let path = std::path::Path::new(template_path);
        let dir = path.parent()?;
        let config_path = dir.join("tokenizer_config.json");

        if config_path.exists() {
            let content = std::fs::read_to_string(config_path).ok()?;
            let config: serde_json::Value = serde_json::from_str(&content).ok()?;

            // Look for chat_template in the config
            if let Some(template) = config.get("chat_template") {
                if let Some(template_str) = template.as_str() {
                    return Some(template_str.to_string());
                }
            }
            return None;
        }
        None
    }

    /// Load chat template from a file (.jinja or .json containing Jinja)
    fn load_chat_template_from_file(template_path: &str) -> Result<Option<String>> {
        use std::fs;
        let content = fs::read_to_string(template_path)
            .map_err(|e| Error::msg(format!("Failed to read chat template file: {}", e)))?;

        // Check if it's a JSON file containing a Jinja template
        if template_path.ends_with(".json") {
            // Parse JSON and extract the template string
            let json_value: serde_json::Value = serde_json::from_str(&content)
                .map_err(|e| Error::msg(format!("Failed to parse chat_template.json: {}", e)))?;

            if let Some(template_str) = json_value.as_str() {
                return Ok(Some(template_str.to_string()));
            } else if let Some(obj) = json_value.as_object() {
                if let Some(template_value) = obj.get("chat_template") {
                    if let Some(template_str) = template_value.as_str() {
                        return Ok(Some(template_str.to_string()));
                    }
                }
            }

            return Err(Error::msg(
                "chat_template.json does not contain a valid template",
            ));
        }

        // Otherwise it's a plain .jinja file
        // Clean up the template (similar to Python implementation)
        let template = content.trim().replace("\\n", "\n");

        Ok(Some(template))
    }

    /// Apply the chat template to a list of messages
    pub fn apply_chat_template(&self, messages: &str, tools: &str, kwargs: &str) -> Result<String> {
        let messages_vec: Vec<serde_json::Value> = if messages.trim().is_empty() {
            Vec::new()
        } else {
            serde_json::from_str(messages)
                .map_err(|e| Error::msg(format!("Failed to parse `messages` JSON: {}", e)))?
        };

        let tools_vec: Vec<serde_json::Value> = if tools.trim().is_empty() {
            Vec::new()
        } else {
            serde_json::from_str(tools)
                .map_err(|e| Error::msg(format!("Failed to parse `tools` JSON: {}", e)))?
        };

        let kwargs_map: HashMap<String, serde_json::Value> = if kwargs.trim().is_empty() {
            HashMap::new()
        } else {
            serde_json::from_str(kwargs)
                .map_err(|e| Error::msg(format!("Failed to parse `kwargs` JSON: {}", e)))?
        };

        if let Some(ref template) = self.chat_template {
            let messages_slice = messages_vec.as_slice();
            let tools_slice = tools_vec.as_slice();
            let params = chat_template::ChatTemplateParams {
                template_kwargs: Some(&kwargs_map),
                add_generation_prompt: true,
                tools: Some(tools_slice),
                ..Default::default()
            };
            match template.apply(messages_slice, params) {
                Ok(s) => Ok(s),
                Err(e) => Err(Error::msg(format!("Error applying chat template: {}", e))),
            }
        } else {
            Err(Error::msg("No chat template available"))
        }
    }
}

impl Encoder for HuggingFaceTokenizer {
    fn encode(&self, input: &str, add_special_tokens: bool) -> Result<Encoding> {
        self.tokenizer
            .encode(input, add_special_tokens)
            .map_err(|e| Error::msg(format!("Encoding failed: {}", e)))
            .map(|encoding| Encoding::Hf(Box::new(encoding)))
    }

    fn encode_batch(&self, inputs: &[&str], add_special_tokens: bool) -> Result<Vec<Encoding>> {
        let encodings = self
            .tokenizer
            .encode_batch(inputs.to_vec(), add_special_tokens)
            .map_err(|e| Error::msg(format!("Batch encoding failed: {}", e)))?;

        Ok(encodings
            .into_iter()
            .map(|e| Encoding::Hf(Box::new(e)))
            .collect())
    }
}

impl Decoder for HuggingFaceTokenizer {
    fn decode(&self, token_ids: &[TokenIdType], skip_special_tokens: bool) -> Result<String> {
        self.tokenizer
            .decode(token_ids, skip_special_tokens)
            .map_err(|e| Error::msg(format!("Decoding failed: {}", e)))
    }
}

impl TokenizerTrait for HuggingFaceTokenizer {
    fn vocab_size(&self) -> usize {
        self.tokenizer.get_vocab_size(false)
    }

    // fn get_special_tokens(&self) -> &SpecialTokens {
    //     &self.special_tokens
    // }

    fn token_to_id(&self, token: &str) -> Option<TokenIdType> {
        self.vocab.get(token).copied()
    }

    fn id_to_token(&self, id: TokenIdType) -> Option<String> {
        self.reverse_vocab.get(&id).cloned()
    }

    fn model_max_length(&self) -> u128 {
        let len = self.config.as_ref().unwrap().model_max_length;
        if len > 0 {
            len
        } else {
            16384
        }
    }

    fn as_any(&self) -> &dyn std::any::Any {
        self
    }
}

#[cfg(test)]
mod tests {
    use crate::{huggingface::HuggingFaceTokenizer, traits::Encoder};

    // These would be integration tests rather than unit tests
    #[test]
    fn test_huggingface_tokenizer() {
        let hf = HuggingFaceTokenizer::from_dir("test/data/deepseek-ai/DeepSeek-R1");
        match hf {
            Err(e) => {
                panic!("Failed to load HuggingFaceTokenizer: {}", e);
            }
            _ => {}
        }
        assert!(hf.is_ok());

        let hf = hf.unwrap();
        assert_eq!(hf.config.as_ref().map(|c| c.model_max_length), Some(16384));

        let bos_token = hf
            .config
            .as_ref()
            .and_then(|c| c.bos_token.as_ref())
            .map(|s| s.as_str());
        assert_eq!(bos_token, Some("<｜begin▁of▁sentence｜>"));

        let tokens = hf.encode("hello world", false).unwrap();
        assert_eq!(tokens.token_ids(), vec![14990, 1879]);
    }
}
