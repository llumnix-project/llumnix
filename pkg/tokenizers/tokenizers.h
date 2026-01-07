#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>

struct tokenizers_buffer {
  uint32_t *ids;
  uint32_t len;
};

const char *tokenizers_version();

void *tokenizers_from_file_with_chat_template(const char *model_dir, const char *chat_template_path, char** error);

char *tokenizers_apply_chat_template(void *ptr, const char *messages, const char *tools, const char *params, char **error);

char *tokenizers_decode(void *ptr, const uint32_t *ids, uint32_t len, bool skip_special_tokens);

struct tokenizers_buffer tokenizers_encode(void *ptr, const char *message, const bool add_special_tokens);

uint32_t tokenizers_vocab_size(void *ptr);

uint64_t tokenizers_model_max_len(void *ptr);

void tokenizers_free_tokenizer(void *ptr);

void tokenizers_free_string(char *string);

void tokenizers_free_buffer(struct tokenizers_buffer buffer);
