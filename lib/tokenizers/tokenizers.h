#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>

// struct tokenizers_encode_options {
//   bool add_special_token;
//   bool return_type_ids;
//   bool return_tokens;
//   bool return_special_tokens_mask;
//   bool return_attention_mask;
//   bool return_offsets;
// };

// struct tokenizers_options {
//   bool encode_special_tokens;
// };

struct tokenizers_buffer {
  uint32_t *ids;
  // uint32_t *type_ids;
  // uint32_t *special_tokens_mask;
  // uint32_t *attention_mask;
  // char *tokens;
  // size_t *offsets;
  uint32_t len;
};

const char *tokenizers_version();

void *tokenizers_from_file_with_chat_template(const char *model_dir, const char *chat_template_path, char** error);

char *tokenizers_apply_chat_template(void *ptr, const char *messages, const char *tools, const char *params, char **error);

char *tokenizers_decode(void *ptr, const uint32_t *ids, uint32_t len, bool skip_special_tokens);

// void *tokenizers_from_tiktoken(const char *model_file, const char *config_file, const char *pattern, char **error);

// struct tokenizers_buffer tokenizers_encode(void *ptr, const char *message, const struct tokenizers_encode_options *options);

// char *tokenizers_decode(void *ptr, const uint32_t *ids, uint32_t len, bool skip_special_tokens);


struct tokenizers_buffer tokenizers_encode(void *ptr, const char *message, const bool add_special_tokens);

uint32_t tokenizers_vocab_size(void *ptr);

uint64_t tokenizers_model_max_len(void *ptr);

void tokenizers_free_tokenizer(void *ptr);

void tokenizers_free_string(char *string);

void tokenizers_free_buffer(struct tokenizers_buffer buffer);




// char *test_null(const char *messages, char **error);