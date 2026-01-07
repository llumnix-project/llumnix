// use minijinja::tests;
use super::huggingface::HuggingFaceTokenizer;
use anyhow::anyhow;
use anyhow::Result;
use tokenizers::{processors::template::TemplateProcessing};

// use super::traits::TokenizerConfig;
use super::traits::{Decoder, Encoder, Encoding, TokenIdType, Tokenizer as TokenizerTrait};

pub struct LlamaTokenizer {
    base_tokenizer: HuggingFaceTokenizer,
    pub use_default_system_prompt: bool,
}

impl LlamaTokenizer {
    pub fn from_file(file_path: &str) -> Result<Self> {
        let base_tokenizer = HuggingFaceTokenizer::from_file(file_path)?;
        let mut llama = LlamaTokenizer {
            base_tokenizer,
            use_default_system_prompt: false,
        };
        llama.update_post_processor()?;
        Ok(llama)
    }

    pub fn from_dir_with_chat_template(
        model_path_str: &str,
        chat_template_path: Option<&str>,
    ) -> Result<Self> {
        let base_tokenizer =
            HuggingFaceTokenizer::from_dir_with_chat_template(model_path_str, chat_template_path)?;
        let mut llama = LlamaTokenizer {
            base_tokenizer,
            use_default_system_prompt: false,
        };
        llama.update_post_processor()?;
        Ok(llama)
    }

    pub fn from_file_with_chat_template(
        file_path: &str,
        chat_template_path: Option<&str>,
    ) -> Result<Self> {
        let base_tokenizer =
            HuggingFaceTokenizer::from_file_with_chat_template(file_path, chat_template_path)?;
        let mut llama = LlamaTokenizer {
            base_tokenizer,
            use_default_system_prompt: false,
        };
        llama.update_post_processor()?;
        Ok(llama)
    }

    pub fn from_dir(model_path_str: &str) -> Result<Self> {
        let base_tokenizer = HuggingFaceTokenizer::from_dir(model_path_str)?;
        let mut llama = LlamaTokenizer {
            base_tokenizer,
            use_default_system_prompt: false,
        };
        llama.update_post_processor()?;
        Ok(llama)
    }

    pub fn inner(&self) -> &HuggingFaceTokenizer {
        &self.base_tokenizer
    }

    fn add_bos_token(&self) -> bool {
        self.base_tokenizer
            .config
            .as_ref()
            .and_then(|c| c.add_bos_token)
            .unwrap_or(false)
    }

    fn add_eos_token(&self) -> bool {
        self.base_tokenizer
            .config
            .as_ref()
            .and_then(|c| c.add_eos_token)
            .unwrap_or(false)
    }

    fn update_post_processor(&mut self) -> Result<()> {
        // Implement Llama-specific post-processor updates here
        let bos = self
            .base_tokenizer
            .config
            .as_ref()
            .and_then(|c| c.bos_token.as_ref())
            .unwrap()
            .as_str();
        let eos = self
            .base_tokenizer
            .config
            .as_ref()
            .and_then(|c| c.eos_token.as_ref())
            .unwrap()
            .as_str();


        fn get_added_token_id(
            tokenizer: &HuggingFaceTokenizer,
            token: &str,
        ) -> Option<TokenIdType> {
            tokenizer
                .tokenizer
                .get_added_vocabulary()
                .token_to_id(token, tokenizer.tokenizer.get_model())
        }
        let bos_token_id = get_added_token_id(&self.base_tokenizer, bos).ok_or_else(|| anyhow!("Failed to get bos_token_id"))?;
        let eos_token_id = get_added_token_id(&self.base_tokenizer, eos).ok_or_else(|| anyhow!("Failed to get eos_token_id"))?;

        // python
        // single = f"{(bos+':0 ') if self.add_bos_token else ''}$A:0{(' '+eos+':0') if self.add_eos_token else ''}"
        // pair = f"{single}{(' '+bos+':1') if self.add_bos_token else ''} $B:1{(' '+eos+':1') if self.add_eos_token else ''}"

        let single = format!(
            "{}$A:0{}",
            if self.add_bos_token() {
                format!("{}:0 ", bos)
            } else {
                "".to_string()
            },
            if self.add_eos_token() {
                format!(" {}:0", eos)
            } else {
                "".to_string()
            }
        );

        let pair = format!(
            "{}{} $B:1{}",
            single,
            if self.add_bos_token() {
                format!(" {}:1", bos)
            } else {
                "".to_string()
            },
            if self.add_eos_token() {
                format!(" {}:1", eos)
            } else {
                "".to_string()
            }
        );

        // python
        // special_tokens = []
        // if self.add_bos_token:
        //     special_tokens.append((bos, bos_token_id))
        // if self.add_eos_token:
        //     special_tokens.append((eos, eos_token_id))
        let mut special_tokens = vec![];
        if self.add_bos_token() {
            special_tokens.push((bos.to_string(), bos_token_id));
        }
        if self.add_eos_token() {
            special_tokens.push((eos.to_string(), eos_token_id));
        }

        let post_processor = TemplateProcessing::builder()
            .try_single(single)
            .unwrap()
            .try_pair(pair)
            .unwrap()
            .special_tokens(special_tokens)
            .build()
            .unwrap();

        self.base_tokenizer
            .tokenizer
            .with_post_processor(Option::Some(post_processor));
        Ok(())
    }
}

impl Encoder for LlamaTokenizer {
    fn encode_batch(&self, inputs: &[&str], add_special_tokens: bool) -> Result<Vec<Encoding>> {
        self.base_tokenizer.encode_batch(inputs, add_special_tokens)
    }

    fn encode(&self, input: &str, add_special_tokens: bool) -> Result<Encoding> {
        self.base_tokenizer.encode(input, add_special_tokens)
    }
}

impl Decoder for LlamaTokenizer {
    fn decode(&self, ids: &[TokenIdType], skip_special_tokens: bool) -> Result<String> {
        self.base_tokenizer.decode(ids, skip_special_tokens)
    }
}

impl TokenizerTrait for LlamaTokenizer {
    fn vocab_size(&self) -> usize {
        self.base_tokenizer.vocab_size()
    }

    fn token_to_id(&self, token: &str) -> Option<TokenIdType> {
        self.base_tokenizer.token_to_id(token)
    }

    fn id_to_token(&self, id: TokenIdType) -> Option<String> {
        self.base_tokenizer.id_to_token(id)
    }

    fn model_max_length(&self) -> u128 {
        self.base_tokenizer.model_max_length()
    }

    fn as_any(&self) -> &dyn std::any::Any {
        self
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::Path;

    #[test]
    fn test_llama_tokenizer() {
        let model_path = Path::new("test/data/deepseek-ai/DeepSeek-R1");
        let tokenizer = LlamaTokenizer::from_dir(model_path.to_str().unwrap()).unwrap();
        let encoding = tokenizer.encode("Hello, world!", true).unwrap();
        assert_eq!(encoding.token_ids()[0], 151646);
        let encoding = tokenizer.encode("Hello, world!", false).unwrap();
        assert_eq!(encoding.token_ids()[0], 9707);
    }
}
