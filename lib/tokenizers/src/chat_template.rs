use crate::protocol::{TextMessage, Tool};
use anyhow::{anyhow, Result};
use chrono::Local;
use minijinja::{context, Environment, ErrorKind, Template, Value};
use minijinja_contrib::pycompat;
use serde::{Deserialize, Serialize};
use std::path::Path;
use std::{collections::HashMap, fs};
/// Raise a exception (custom function) used in the chat templates
pub(crate) fn raise_exception(err_text: String) -> Result<String, minijinja::Error> {
    Err(minijinja::Error::new(ErrorKind::SyntaxError, err_text))
}

/// Get the current date in a specific format (custom function), similar to `datetime.now().strftime()` in Python
pub(crate) fn strftime_now(format_str: String) -> Result<String, minijinja::Error> {
    Ok(Local::now().format(&format_str).to_string())
}

/// Helper function to discover chat template files in a directory
pub fn discover_chat_template_in_dir(dir: &Path) -> Option<String> {
    use std::fs;

    // Priority 1: Look for tokenizer_config.json (contains Jinja in JSON format)
    let config_path = dir.join("tokenizer_config.json");
    if config_path.exists() {
        return config_path.to_str().map(|s| s.to_string());
    }

    // Priority 1: Look for chat_template.json (contains Jinja in JSON format)
    let json_template_path = dir.join("chat_template.json");
    if json_template_path.exists() {
        return json_template_path.to_str().map(|s| s.to_string());
    }

    // Priority 2: Look for chat_template.jinja (standard Jinja file)
    let jinja_path = dir.join("chat_template.jinja");
    if jinja_path.exists() {
        return jinja_path.to_str().map(|s| s.to_string());
    }

    // Priority 3: Look for any .jinja file (for models with non-standard naming)
    if let Ok(entries) = fs::read_dir(dir) {
        for entry in entries.flatten() {
            if let Some(name) = entry.file_name().to_str() {
                if name.ends_with(".jinja") && name != "chat_template.jinja" {
                    return entry.path().to_str().map(|s| s.to_string());
                }
            }
        }
    }

    None
}

#[derive(Debug, Clone)]
pub struct ChatTemplate {
    template: Template<'static, 'static>,
    template_kwargs: Option<HashMap<String, serde_json::Value>>,
}

/// Parameters for chat template application
#[derive(Clone, Serialize, Default)]
pub struct ChatTemplateParams<'a> {
    pub add_generation_prompt: bool,
    pub tools: Option<&'a [serde_json::Value]>,
    // pub documents: Option<&'a [serde_json::Value]>,
    pub template_kwargs: Option<&'a HashMap<String, serde_json::Value>>,
}

impl ChatTemplate {
    pub fn new(
        template: String,
        template_kwargs: Option<HashMap<String, serde_json::Value>>,
    ) -> Self {
        let mut env = Box::new(Environment::new());
        // enable things like .strip() or .capitalize()
        env.set_unknown_method_callback(pycompat::unknown_method_callback);

        let template_str = ChatTemplate::hack_template(&template).into_boxed_str();
        env.add_function("raise_exception", raise_exception);
        env.add_function("strftime_now", strftime_now);
        tracing::debug!("Loading template: {}", template_str);

        // leaking env and template_str as read-only, static resources for performance.
        let template = Box::leak(env)
            .template_from_str(Box::leak(template_str))
            .unwrap();

        Self {
            template,
            template_kwargs,
        }
    }

    fn hack_template(template: &str) -> String {
        // TODO: replace with better solution
        // hack to adjust gemma3 template for debug
        // replace 'messages[0]['content'][0]['text']' with 'messages[0]['content']'
        let mutated_template = template.replace(
            "messages[0]['content'][0]['text']",
            "messages[0]['content']",
        );
        //  Hack to fix Qwen3 templating.
        //  It uses python notation to reverse lists, which do not exist in minijinja
        //  so we're using the reverse filter instead.
        let mutated_template = mutated_template.replace("[::-1]", "|reverse");
        // TODO: replace with a better solution
        // Hack to remove the {% generation %} and {% endgeneration %} statements from
        // the Jinja2 chat templates if there, since those are only using for assistant
        // masking during training, and should be ignored during inference
        let mutated_template = mutated_template.replace("{% generation %}", "");
        let mutated_template = mutated_template.replace("{% endgeneration %}", "");
        tracing::debug!("Mutated template: {}", mutated_template);
        mutated_template
    }

    pub fn apply(
        &self,
        messages: &[serde_json::Value],
        params: ChatTemplateParams,
    ) -> Result<String, Box<dyn std::error::Error>> {
        let mut final_kwargs = self.template_kwargs.clone().unwrap_or_default();

        if let Some(extra_kwargs) = params.template_kwargs {
            final_kwargs.extend(extra_kwargs.clone());
        }

        let ctx = context! {
            messages => messages,
            add_generation_prompt => params.add_generation_prompt,
            tools =>  params.tools.unwrap_or_else(|| &[]),
            ..Value::from_serialize(&final_kwargs),
        };

        let rendered = self
            .template
            .render(&ctx)
            .map_err(|e| anyhow!("Failed to render template: {}", e))?;

        Ok(rendered)
    }
}

#[derive(Clone, Serialize, Deserialize, Default)]
pub(crate) struct ChatTemplateInputs<'a> {
    messages: Vec<TextMessage>,
    bos_token: Option<&'a str>,
    eos_token: Option<&'a str>,
    add_generation_prompt: bool,
    tools: Option<Vec<Tool>>,
}

// tests
#[cfg(test)]
mod tests {
    use crate::chat_template::ChatTemplateInputs;
    use std::path::PathBuf;

    use crate::chat_template::ChatTemplate;
    use crate::chat_template::{raise_exception, strftime_now};
    use crate::chat_template::{ChatTemplateParams, TextMessage, Tool};
    use crate::protocol::{Message, MessageBody, MessageChunk, MessageContent, Url};
    use crate::traits::TokenizerConfig;
    use chrono::Local;
    use minijinja::{context, Environment, Value};
    use std::collections::HashMap;

    #[test]
    fn test_chat_template() {
        let env = Environment::new();

        let source = r#"
        {% for message in messages %}
            {% if message['role'] == 'system' %}
                {% if message['content']%}
                    {{'### System:\n' + message['content']+'\n\n'}}
                {% endif %}
            {% elif message['role'] == 'user' %}
                {{'### User:\n' + message['content']+'\n\n'}}
            {% elif message['role'] == 'assistant' %}
                {{'### Assistant:\n'  + message['content']}}
            {% endif %}
            {% if loop.last and add_generation_prompt %}
                {{ '### Assistant:\n' }}
            {% endif %}
        {% endfor %}"#;

        // trim all the whitespace
        let source = source
            .lines()
            .map(|line| line.trim())
            .collect::<Vec<&str>>()
            .join("");

        let tmpl = env.template_from_str(&source);

        let messages = vec![
            TextMessage {
                role: "user".to_string(),
                content: "Hi!".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "Hello how can I help?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "user".to_string(),
                content: "What is Deep Learning?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "magic!".to_string(),
                ..Default::default()
            },
        ];

        let kwargs = serde_json::json!({
            "bos_token": "[BOS]",
            "eos_token": "[EOS]",
        });

        let ctx = context! {
            messages => serde_json::to_value(&messages).unwrap(),
            add_generation_prompt => true,
            ..Value::from_serialize(&kwargs),
        };

        let result = tmpl.unwrap().render(ctx).unwrap();

        assert_eq!(
            result,
            "### User:\nHi!\n\n### Assistant:\nHello how can I help?### User:\nWhat is Deep Learning?\n\n### Assistant:\nmagic!### Assistant:\n"
        );
    }

    #[test]
    fn test_chat_template_with_tool_response() {
        let env = Environment::new();

        // template modified from Llama-3.1-8B-Instruct
        // https://huggingface.co/meta-llama/Llama-3.1-8B-Instruct/blob/0e9e39f249a16976918f6564b8830bc894c89659/tokenizer_config.json#L2053
        // the main change is accesing `message.tool_call_id` from the messages
        let source = r#"
        {{- bos_token }}
        {%- if custom_tools is defined %}
            {%- set tools = custom_tools %}
        {%- endif %}
        {%- if not tools_in_user_message is defined %}
            {%- set tools_in_user_message = true %}
        {%- endif %}
        {%- if not date_string is defined %}
            {%- set date_string = "26 Jul 2024" %}
        {%- endif %}
        {%- if not tools is defined %}
            {%- set tools = none %}
        {%- endif %}

        {#- This block extracts the system message, so we can slot it into the right place. #}
        {%- if messages[0]['role'] == 'system' %}
            {%- set system_message = messages[0]['content']|trim %}
            {%- set messages = messages[1:] %}
        {%- else %}
            {%- set system_message = "" %}
        {%- endif %}

        {#- System message + builtin tools #}
        {{- "<|start_header_id|>system<|end_header_id|>\n\n" }}
        {%- if builtin_tools is defined or tools is not none %}
            {{- "Environment: ipython\n" }}
        {%- endif %}
        {%- if builtin_tools is defined %}
            {{- "Tools: " + builtin_tools | reject('equalto', 'code_interpreter') | join(", ") + "\n\n"}}
        {%- endif %}
        {{- "Cutting Knowledge Date: December 2023\n" }}
        {{- "Today Date: " + date_string + "\n\n" }}
        {%- if tools is not none and not tools_in_user_message %}
            {{- "You have access to the following functions. To call a function, please respond with JSON for a function call." }}
            {{- 'Respond in the format {"name": function name, "parameters": dictionary of argument name and its value}.' }}
            {{- "Do not use variables.\n\n" }}
            {%- for t in tools %}
                {{- t | tojson(indent=4) }}
                {{- "\n\n" }}
            {%- endfor %}
        {%- endif %}
        {{- system_message }}
        {{- "<|eot_id|>" }}

        {#- Custom tools are passed in a user message with some extra guidance #}
        {%- if tools_in_user_message and not tools is none %}
            {#- Extract the first user message so we can plug it in here #}
            {%- if messages | length != 0 %}
                {%- set first_user_message = messages[0]['content']|trim %}
                {%- set messages = messages[1:] %}
            {%- else %}
                {{- raise_exception("Cannot put tools in the first user message when there's no first user message!") }}
        {%- endif %}
            {{- '<|start_header_id|>user<|end_header_id|>\n\n' -}}
            {{- "Given the following functions, please respond with a JSON for a function call " }}
            {{- "with its proper arguments that best answers the given prompt.\n\n" }}
            {{- 'Respond in the format {"name": function name, "parameters": dictionary of argument name and its value}.' }}
            {{- "Do not use variables.\n\n" }}
            {%- for t in tools %}
                {{- t | tojson(indent=4) }}
                {{- "\n\n" }}
            {%- endfor %}
            {{- first_user_message + "<|eot_id|>"}}
        {%- endif %}

        {%- for message in messages %}
            {%- if not (message.role == 'ipython' or message.role == 'tool' or 'tool_calls' in message) %}
                {{- '<|start_header_id|>' + message['role'] + '<|end_header_id|>\n\n'+ message['content'] | trim + '<|eot_id|>' }}
            {%- elif 'tool_calls' in message %}
                {%- if not message.tool_calls|length == 1 %}
                    {{- raise_exception("This model only supports single tool-calls at once!") }}
                {%- endif %}
                {%- set tool_call = message.tool_calls[0].function %}
                {%- if builtin_tools is defined and tool_call.name in builtin_tools %}
                    {{- '<|start_header_id|>assistant<|end_header_id|>\n\n' -}}
                    {{- "<|python_tag|>" + tool_call.name + ".call(" }}
                    {%- for arg_name, arg_val in tool_call.arguments | items %}
                        {{- arg_name + '="' + arg_val + '"' }}
                        {%- if not loop.last %}
                            {{- ", " }}
                        {%- endif %}
                        {%- endfor %}
                    {{- ")" }}
                {%- else  %}
                    {{- '<|start_header_id|>assistant<|end_header_id|>\n\n' -}}
                    {{- '{"name": "' + tool_call.name + '", ' }}
                    {{- '"parameters": ' }}
                    {{- tool_call.arguments | tojson }}
                    {{- "}" }}
                {%- endif %}
                {%- if builtin_tools is defined %}
                    {#- This means we're in ipython mode #}
                    {{- "<|eom_id|>" }}
                {%- else %}
                    {{- "<|eot_id|>" }}
                {%- endif %}
            {%- elif message.role == "tool" or message.role == "ipython" %}
                {{- "<|start_header_id|>ipython<|end_header_id|>\n\n" }}
                    {{- "TOOL CALL ID: " + message.tool_call_id + "\n\n" }}
                {%- if message.content is mapping or message.content is iterable %}
                    {{- message.content | tojson }}
                {%- else %}
                    {{- message.content }}
                {%- endif %}
                {{- "<|eot_id|>" }}
            {%- endif %}
        {%- endfor %}
        {%- if add_generation_prompt %}
            {{- '<|start_header_id|>assistant<|end_header_id|>\n\n' }}
        {%- endif %}
        "#;

        // trim all the whitespace
        let source = source
            .lines()
            .map(|line| line.trim())
            .collect::<Vec<&str>>()
            .join("");

        let tmpl = env.template_from_str(&source);

        let messages = vec![
            TextMessage {
                role: "user".to_string(),
                content: "Hi!".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: r#"[ { "id": "0", "function": { "arguments": '{"longitude": 2.2945, "latitude": 48.8567}', "name": "get_weather", "description": None, }, "type": "function", } ]"#.to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "tool".to_string(),
                content: "6.7".to_string(),
                tool_call_id: Some("0".to_string()),
            },
        ];

        let kwargs = serde_json::json!({
            "bos_token": "[BOS]",
            "eos_token": "[EOS]",
        });
        let ctx = context! {
            messages => serde_json::to_value(&messages).unwrap(),
            add_generation_prompt => true,
            ..Value::from_serialize(&kwargs),

        };

        let result = tmpl.unwrap().render(ctx).unwrap();

        assert_eq!(
            result,
            r#"[BOS]<|start_header_id|>system<|end_header_id|>

Cutting Knowledge Date: December 2023
Today Date: 26 Jul 2024

<|eot_id|><|start_header_id|>user<|end_header_id|>

Hi!<|eot_id|><|start_header_id|>assistant<|end_header_id|>

[ { "id": "0", "function": { "arguments": '{"longitude": 2.2945, "latitude": 48.8567}', "name": "get_weather", "description": None, }, "type": "function", } ]<|eot_id|><|start_header_id|>ipython<|end_header_id|>

TOOL CALL ID: 0

"6.7"<|eot_id|><|start_header_id|>assistant<|end_header_id|>

"#
        );
    }

    #[test]
    fn test_chat_template_loop_controls() {
        // some chat templates as e.g. CohereForAI/c4ai-command-r7b-12-202 contain `break`
        // statements in their chat templates, so the feature `loop_controls` has been included
        // in `minijinja`
        let env = Environment::new();

        let source = r#"
        {% set user_count = 0 %}
        {% for message in messages %}
            {% if message['role'] == 'user' %}
                {{'### User:\n' + message['content']+'\n\n'}}
                {% set user_count = user_count + 1 %}
                {% if user_count >= 2 %}
                    {% break %}
                {% endif %}
            {% elif message['role'] == 'assistant' %}
                {{'### Assistant:\n'  + message['content']}}
            {% endif %}
        {% endfor %}
        {% if add_generation_prompt %}
            {{ '### Assistant:\n' }}
        {% endif %}"#;

        // trim all the whitespace
        let source = source
            .lines()
            .map(|line| line.trim())
            .collect::<Vec<&str>>()
            .join("");

        let tmpl = env.template_from_str(&source);

        let messages = vec![
            TextMessage {
                role: "user".to_string(),
                content: "Hi!".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "Hello how can I help?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "user".to_string(),
                content: "What is Deep Learning?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "magic!".to_string(),
                ..Default::default()
            },
        ];

        let kwargs = serde_json::json!({
            "bos_token": "[BOS]",
            "eos_token": "[EOS]",
        });
        let ctx = context! {
            messages => serde_json::to_value(&messages).unwrap(),
            add_generation_prompt => true,
            ..Value::from_serialize(&kwargs),
        };

        let result = tmpl.unwrap().render(ctx).unwrap();

        assert_eq!(
            result,
            "### User:\nHi!\n\n### Assistant:\nHello how can I help?### User:\nWhat is Deep Learning?\n\n### Assistant:\n"
        );
    }

    #[test]
    fn test_chat_template_invalid_with_raise() {
        let mut env = Environment::new();
        env.add_function("raise_exception", raise_exception);
        env.add_function("strftime_now", strftime_now);

        let source = r#"
        {{ bos_token }}
        {% for message in messages %}
        {% if (message['role'] == 'user') != (loop.index0 % 2 == 0) %}
        {{ raise_exception('Conversation roles must alternate user/assistant/user/assistant/...') }}
        {% endif %}
        {% if message['role'] == 'user' %}
        {{ '[INST] ' + message['content'] + ' [/INST]' }}
        {% elif message['role'] == 'assistant' %}
        {{ message['content'] + eos_token}}
        {% else %}
        {{ raise_exception('Only user and assistant roles are supported!') }}
        {% endif %}
        {% endfor %}"#;

        // trim all the whitespace
        let source = source
            .lines()
            .map(|line| line.trim())
            .collect::<Vec<&str>>()
            .join("");

        let tmpl = env.template_from_str(&source);

        // let chat_template_inputs = ChatTemplateInputs {
        let messages = vec![
            TextMessage {
                role: "user".to_string(),
                content: "Hi!".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "user".to_string(),
                content: "Hi again!".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "Hello how can I help?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "user".to_string(),
                content: "What is Deep Learning?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "magic!".to_string(),
                ..Default::default()
            },
        ];

        let kwargs = serde_json::json!({
            "bos_token": "[BOS]",
            "eos_token": "[EOS]",
        });
        let ctx = context! {
            messages => serde_json::to_value(&messages).unwrap(),
            add_generation_prompt => true,
            ..Value::from_serialize(&kwargs),
        };

        let result = tmpl.unwrap().render(ctx); //.err().unwrap();

        match result {
            Ok(_) => panic!("Should have failed"),
            Err(e) => {
                assert_eq!(
                    e.detail().unwrap(),
                    "Conversation roles must alternate user/assistant/user/assistant/..."
                );
            }
        }
    }

    #[test]
    fn test_chat_template_valid_with_raise() {
        let mut env = Environment::new();
        env.add_function("raise_exception", raise_exception);
        env.add_function("strftime_now", strftime_now);

        let source = r#"
        {{ bos_token }}
        {% for message in messages %}
        {% if (message['role'] == 'user') != (loop.index0 % 2 == 0) %}
        {{ raise_exception('Conversation roles must alternate user/assistant/user/assistant/...') }}
        {% endif %}
        {% if message['role'] == 'user' %}
        {{ '[INST] ' + message['content'] + ' [/INST]' }}
        {% elif message['role'] == 'assistant' %}
        {{ message['content'] + eos_token}}
        {% else %}
        {{ raise_exception('Only user and assistant roles are supported!') }}
        {% endif %}
        {% endfor %}"#;

        // trim all the whitespace
        let source = source
            .lines()
            .map(|line| line.trim())
            .collect::<Vec<&str>>()
            .join("");

        let tmpl = env.template_from_str(&source);

        let messages = vec![
            TextMessage {
                role: "user".to_string(),
                content: "Hi!".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "Hello how can I help?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "user".to_string(),
                content: "What is Deep Learning?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "magic!".to_string(),
                ..Default::default()
            },
        ];

        let kwargs = serde_json::json!({
            "bos_token": "[BOS]",
            "eos_token": "[EOS]",
        });
        let ctx = context! {
            messages => serde_json::to_value(&messages).unwrap(),
            add_generation_prompt => true,
            ..Value::from_serialize(&kwargs),
        };

        let result = tmpl.unwrap().render(ctx).unwrap();
        assert_eq!(result, "[BOS][INST] Hi! [/INST]Hello how can I help?[EOS][INST] What is Deep Learning? [/INST]magic![EOS]");
    }

    #[test]
    fn test_chat_template_valid_with_strftime_now() {
        let mut env = Environment::new();
        env.add_function("raise_exception", raise_exception);
        env.add_function("strftime_now", strftime_now);

        let source = r#"
        {% set today = strftime_now("%Y-%m-%d") %}
        {% set default_system_message = "The current date is " + today + "." %}
        {{ bos_token }}
        {% if messages[0]['role'] == 'system' %}
            { set system_message = messages[0]['content'] %}
            {%- set loop_messages = messages[1:] %}
        {% else %}
            {%- set system_message = default_system_message %}
            {%- set loop_messages = messages %}
        {% endif %}
        {{ '[SYSTEM_PROMPT]' + system_message + '[/SYSTEM_PROMPT]' }}
        {% for message in loop_messages %}
            {% if message['role'] == 'user' %}
                {{ '[INST]' + message['content'] + '[/INST]' }}
            {% elif message['role'] == 'assistant' %}
                {{ message['content'] + eos_token }}
            {% else %}
                {{ raise_exception('Only user and assistant roles are supported!') }}
            {% endif %}
        {% endfor %}
        "#;

        // trim all the whitespace
        let source = source
            .lines()
            .map(|line| line.trim())
            .collect::<Vec<&str>>()
            .join("");

        let tmpl = env.template_from_str(&source);

        let messages = vec![
            TextMessage {
                role: "user".to_string(),
                content: "Hi!".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "Hello how can I help?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "user".to_string(),
                content: "What is Deep Learning?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "magic!".to_string(),
                ..Default::default()
            },
        ];

        let kwargs = serde_json::json!({
            "bos_token": "[BOS]",
            "eos_token": "[EOS]",
        });
        let ctx = context! {
            messages => serde_json::to_value(&messages).unwrap(),
            add_generation_prompt => true,
            ..Value::from_serialize(&kwargs),
        };
        let current_date = Local::now().format("%Y-%m-%d").to_string();
        let result = tmpl.unwrap().render(ctx).unwrap();
        assert_eq!(result, format!("[BOS][SYSTEM_PROMPT]The current date is {}.[/SYSTEM_PROMPT][INST]Hi![/INST]Hello how can I help?[EOS][INST]What is Deep Learning?[/INST]magic![EOS]", current_date));
    }

    #[test]
    fn test_chat_template_valid_with_add_generation_prompt() {
        let mut env = Environment::new();
        env.add_function("raise_exception", raise_exception);
        env.add_function("strftime_now", strftime_now);

        let source = r#"
        {% for message in messages %}
        {{'<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>' + '\n'}}
        {% endfor %}
        {% if add_generation_prompt %}
            {{ '<|im_start|>assistant\n' }}
        {% endif %}"#;

        // trim all the whitespace
        let source = source
            .lines()
            .map(|line| line.trim())
            .collect::<Vec<&str>>()
            .join("");

        let tmpl = env.template_from_str(&source);

        let messages = vec![
            TextMessage {
                role: "user".to_string(),
                content: "Hi!".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "Hello how can I help?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "user".to_string(),
                content: "What is Deep Learning?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "magic!".to_string(),
                ..Default::default()
            },
        ];
        let kwargs = serde_json::json!({
            "bos_token": "[BOS]",
            "eos_token": "[EOS]",
        });
        let ctx = context! {
            messages => serde_json::to_value(&messages).unwrap(),
            add_generation_prompt => true,
            ..Value::from_serialize(&kwargs),
        };

        let result = tmpl.unwrap().render(ctx).unwrap();
        assert_eq!(result, "<|im_start|>user\nHi!<|im_end|>\n<|im_start|>assistant\nHello how can I help?<|im_end|>\n<|im_start|>user\nWhat is Deep Learning?<|im_end|>\n<|im_start|>assistant\nmagic!<|im_end|>\n<|im_start|>assistant\n");
    }

    struct ChatTemplateTestItem {
        name: &'static str,
        chat_template: &'static str,
        input: ChatTemplateInputs<'static>,
        target: &'static str,
    }

    #[test]
    fn test_many_chat_templates() {
        let example_chat = vec![
            TextMessage {
                role: "user".to_string(),
                content: "Hello, how are you?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "assistant".to_string(),
                content: "I'm doing great. How can I help you today?".to_string(),
                ..Default::default()
            },
            TextMessage {
                role: "user".to_string(),
                content: "I'd like to show off how chat templating works!".to_string(),
                ..Default::default()
            },
        ];

        let example_chat_with_system = [TextMessage {
            role: "system".to_string(),
            content: "You are a friendly chatbot who always responds in the style of a pirate"
                .to_string(),
            ..Default::default()
        }]
        .iter()
        .chain(&example_chat)
        .cloned()
        .collect::<Vec<_>>();

        let test_default_templates = vec![
            ChatTemplateTestItem {
                name: "_base",
                chat_template: "{% for message in messages %}{{'<|im_start|>' + message['role'] + '\\n' + message['content'] + '<|im_end|>' + '\\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\\n' }}{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some(""),
                    eos_token: Some(""),
                    ..Default::default()
                },
                target: "<|im_start|>user\nHello, how are you?<|im_end|>\n<|im_start|>assistant\nI'm doing great. How can I help you today?<|im_end|>\n<|im_start|>user\nI'd like to show off how chat templating works!<|im_end|>\n",
            },
            ChatTemplateTestItem {
                name: "blenderbot",
                chat_template: "{% for message in messages %}{% if message['role'] == 'user' %}{{ ' ' }}{% endif %}{{ message['content'] }}{% if not loop.last %}{{ '  ' }}{% endif %}{% endfor %}{{ eos_token }}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some(""),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: " Hello, how are you?  I'm doing great. How can I help you today?   I'd like to show off how chat templating works!</s>",
            },
            ChatTemplateTestItem {
                name: "blenderbot_small",
                chat_template: "{% for message in messages %}{% if message['role'] == 'user' %}{{ ' ' }}{% endif %}{{ message['content'] }}{% if not loop.last %}{{ '  ' }}{% endif %}{% endfor %}{{ eos_token }}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some(""),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: " Hello, how are you?  I'm doing great. How can I help you today?   I'd like to show off how chat templating works!</s>",
            },
            ChatTemplateTestItem {
                name: "bloom",
                chat_template: "{% for message in messages %}{{ message.content }}{{ eos_token }}{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some(""),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "Hello, how are you?</s>I'm doing great. How can I help you today?</s>I'd like to show off how chat templating works!</s>",
            },
            ChatTemplateTestItem {
                name: "gpt_neox",
                chat_template: "{% for message in messages %}{{ message.content }}{{ eos_token }}{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some(""),
                    eos_token: Some("<|endoftext|>"),
                    ..Default::default()
                },
                target: "Hello, how are you?<|endoftext|>I'm doing great. How can I help you today?<|endoftext|>I'd like to show off how chat templating works!<|endoftext|>",
            },
            ChatTemplateTestItem {
                name: "gpt2",
                chat_template: "{% for message in messages %}{{ message.content }}{{ eos_token }}{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some(""),
                    eos_token: Some("<|endoftext|>"),
                    ..Default::default()
                },
                target: "Hello, how are you?<|endoftext|>I'm doing great. How can I help you today?<|endoftext|>I'd like to show off how chat templating works!<|endoftext|>",
            },
            ChatTemplateTestItem {
                name: "llama",
                // NOTE: the `.strip()` has been replaced with `| trim` in the following template
                chat_template: "{% if messages[0]['role'] == 'system' %}{% set loop_messages = messages[1:] %}{% set system_message = messages[0]['content'] %}{% elif USE_DEFAULT_PROMPT == true and not '<<SYS>>' in messages[0]['content'] %}{% set loop_messages = messages %}{% set system_message = 'DEFAULT_SYSTEM_MESSAGE' %}{% else %}{% set loop_messages = messages %}{% set system_message = false %}{% endif %}{% for message in loop_messages %}{% if (message['role'] == 'user') != (loop.index0 % 2 == 0) %}{{ raise_exception('Conversation roles must alternate user/assistant/user/assistant/...') }}{% endif %}{% if loop.index0 == 0 and system_message != false %}{% set content = '<<SYS>>\\n' + system_message + '\\n<</SYS>>\\n\\n' + message['content'] %}{% else %}{% set content = message['content'] %}{% endif %}{% if message['role'] == 'user' %}{{ bos_token +'[INST] ' + content | trim + ' [/INST]' }}{% elif message['role'] == 'system' %}{{ '<<SYS>>\\n' + content | trim + '\\n<</SYS>>\\n\\n' }}{% elif message['role'] == 'assistant' %}{{ ' ' + content | trim + ' ' + eos_token }}{% endif %}{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat_with_system.clone(),
                    add_generation_prompt: true,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<s>[INST] <<SYS>>\nYou are a friendly chatbot who always responds in the style of a pirate\n<</SYS>>\n\nHello, how are you? [/INST] I'm doing great. How can I help you today? </s><s>[INST] I'd like to show off how chat templating works! [/INST]",
            },
            ChatTemplateTestItem {
                name: "whisper",
                chat_template: "{% for message in messages %}{{ message.content }}{{ eos_token }}{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: true,
                    bos_token: Some(""),
                    eos_token: Some("<|endoftext|>"),
                    ..Default::default()
                },
                target: "Hello, how are you?<|endoftext|>I'm doing great. How can I help you today?<|endoftext|>I'd like to show off how chat templating works!<|endoftext|>",
            },
        ];

        #[allow(unused_variables)] // name is unused
        for ChatTemplateTestItem {
            name,
            chat_template,
            input,
            target,
        } in test_default_templates
        {
            let mut env = Environment::new();
            env.add_function("raise_exception", raise_exception);
            env.add_function("strftime_now", strftime_now);
            let tmpl = env.template_from_str(chat_template);
            let result = tmpl.unwrap().render(input).unwrap();
            assert_eq!(result, target);
        }

        let test_custom_templates = vec![
            ChatTemplateTestItem {
                name: "HuggingFaceH4/zephyr-7b-beta (add_generation_prompt=false)",
                chat_template: "{% for message in messages %}\n{% if message['role'] == 'user' %}\n{{ '<|user|>\\n' + message['content'] + eos_token }}\n{% elif message['role'] == 'system' %}\n{{ '<|system|>\\n' + message['content'] + eos_token }}\n{% elif message['role'] == 'assistant' %}\n{{ '<|assistant|>\\n'  + message['content'] + eos_token }}\n{% endif %}\n{% if loop.last and add_generation_prompt %}\n{{ '<|assistant|>' }}\n{% endif %}\n{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat_with_system.clone(),
                    add_generation_prompt: false,
                    bos_token: Some(""),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<|system|>\nYou are a friendly chatbot who always responds in the style of a pirate</s><|user|>\nHello, how are you?</s><|assistant|>\nI'm doing great. How can I help you today?</s><|user|>\nI'd like to show off how chat templating works!</s>",
            },
            ChatTemplateTestItem {
                name: "HuggingFaceH4/zephyr-7b-beta (add_generation_prompt=true)",
                chat_template: "{% for message in messages %}\n{% if message['role'] == 'user' %}\n{{ '<|user|>\\n' + message['content'] + eos_token }}\n{% elif message['role'] == 'system' %}\n{{ '<|system|>\\n' + message['content'] + eos_token }}\n{% elif message['role'] == 'assistant' %}\n{{ '<|assistant|>\\n'  + message['content'] + eos_token }}\n{% endif %}\n{% if loop.last and add_generation_prompt %}\n{{ '<|assistant|>' }}\n{% endif %}\n{% endfor %}",
                input: ChatTemplateInputs {
                    messages: vec![
                        TextMessage {
                            role: "system".to_string(),
                            content: "You are a friendly chatbot who always responds in the style of a pirate".to_string(),
                            ..Default::default()
                        },
                        TextMessage {
                            role: "user".to_string(),
                            content: "How many helicopters can a human eat in one sitting?".to_string(),
                            ..Default::default()
                        },
                    ],
                    add_generation_prompt: true,
                    bos_token: Some(""),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<|system|>\nYou are a friendly chatbot who always responds in the style of a pirate</s><|user|>\nHow many helicopters can a human eat in one sitting?</s><|assistant|>",
            },
            ChatTemplateTestItem {
                name: "HuggingFaceH4/zephyr-7b-gemma-v0.1",
                chat_template: "{% if messages[0]['role'] == 'user' or messages[0]['role'] == 'system' %}{{ bos_token }}{% endif %}{% for message in messages %}{{ '<|im_start|>' + message['role'] + '\\n' + message['content'] + '<|im_end|>' + '\\n' }}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% elif messages[-1]['role'] == 'assistant' %}{{ eos_token }}{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<bos>"),
                    eos_token: Some("<eos>"),
                    ..Default::default()
                },
                target: "<bos><|im_start|>user\nHello, how are you?<|im_end|>\n<|im_start|>assistant\nI'm doing great. How can I help you today?<|im_end|>\n<|im_start|>user\nI'd like to show off how chat templating works!<|im_end|>\n",
            },
            ChatTemplateTestItem {
                name: "mistralai/Mistral-7B-Instruct-v0.1",
                chat_template: "{{ bos_token }}{% for message in messages %}{% if (message['role'] == 'user') != (loop.index0 % 2 == 0) %}{{ raise_exception('Conversation roles must alternate user/assistant/user/assistant/...') }}{% endif %}{% if message['role'] == 'user' %}{{ '[INST] ' + message['content'] + ' [/INST]' }}{% elif message['role'] == 'assistant' %}{{ message['content'] + eos_token + ' ' }}{% else %}{{ raise_exception('Only user and assistant roles are supported!') }}{% endif %}{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<s>[INST] Hello, how are you? [/INST]I'm doing great. How can I help you today?</s> [INST] I'd like to show off how chat templating works! [/INST]",
            },
            ChatTemplateTestItem {
                name: "mistralai/Mixtral-8x7B-Instruct-v0.1",
                chat_template: "{{ bos_token }}{% for message in messages %}{% if (message['role'] == 'user') != (loop.index0 % 2 == 0) %}{{ raise_exception('Conversation roles must alternate user/assistant/user/assistant/...') }}{% endif %}{% if message['role'] == 'user' %}{{ '[INST] ' + message['content'] + ' [/INST]' }}{% elif message['role'] == 'assistant' %}{{ message['content'] + eos_token}}{% else %}{{ raise_exception('Only user and assistant roles are supported!') }}{% endif %}{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<s>[INST] Hello, how are you? [/INST]I'm doing great. How can I help you today?</s>[INST] I'd like to show off how chat templating works! [/INST]",
            },
            ChatTemplateTestItem {
                name: "cognitivecomputations/dolphin-2.5-mixtral-8x7b",
                chat_template: "{% if not add_generation_prompt is defined %}{% set add_generation_prompt = false %}{% endif %}{% for message in messages %}{{'<|im_start|>' + message['role'] + '\\n' + message['content'] + '<|im_end|>' + '\\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\\n' }}{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<|im_start|>user\nHello, how are you?<|im_end|>\n<|im_start|>assistant\nI'm doing great. How can I help you today?<|im_end|>\n<|im_start|>user\nI'd like to show off how chat templating works!<|im_end|>\n",
            },
            ChatTemplateTestItem {
                name: "openchat/openchat-3.5-0106",
                // `.title()` has been replaced with `| upper` in the following template
                chat_template: "{{ bos_token }}{% for message in messages %}{{ 'GPT4 Correct ' + (message['role'] | title) + ': ' + message['content'] + '<|end_of_turn|>'}}{% endfor %}{% if add_generation_prompt %}{{ 'GPT4 Correct Assistant:' }}{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<s>GPT4 Correct User: Hello, how are you?<|end_of_turn|>GPT4 Correct Assistant: I'm doing great. How can I help you today?<|end_of_turn|>GPT4 Correct User: I'd like to show off how chat templating works!<|end_of_turn|>",
            },
            ChatTemplateTestItem {
                name: "upstage/SOLAR-10.7B-Instruct-v1.0",
                chat_template: "{% for message in messages %}{{ message.content }}{{ eos_token }}{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "Hello, how are you?</s>I'm doing great. How can I help you today?</s>I'd like to show off how chat templating works!</s>",
            },
            ChatTemplateTestItem {
                name: "codellama/CodeLlama-70b-Instruct-hf",
                // NOTE: `.strip()` has been replaced with `| trim` in the following template
                chat_template: "{% if messages[0]['role'] == 'system' %}{% set user_index = 1 %}{% else %}{% set user_index = 0 %}{% endif %}{% for message in messages %}{% if (message['role'] == 'user') != ((loop.index0 + user_index) % 2 == 0) %}{{ raise_exception('Conversation roles must alternate user/assistant/user/assistant/...') }}{% endif %}{% if loop.index0 == 0 %}{{ '<s>' }}{% endif %}{% set content = 'Source: ' + message['role'] + '\\n\\n ' + message['content'] | trim %}{{ content + ' <step> ' }}{% endfor %}{{'Source: assistant\\nDestination: user\\n\\n '}}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<s>Source: user\n\n Hello, how are you? <step> Source: assistant\n\n I'm doing great. How can I help you today? <step> Source: user\n\n I'd like to show off how chat templating works! <step> Source: assistant\nDestination: user\n\n ",
            },
            ChatTemplateTestItem {
                name: "Deci/DeciLM-7B-instruct",
                chat_template: "{% for message in messages %}\n{% if message['role'] == 'user' %}\n{{ '### User:\\n' + message['content'] }}\n{% elif message['role'] == 'system' %}\n{{ '### System:\\n' + message['content'] }}\n{% elif message['role'] == 'assistant' %}\n{{ '### Assistant:\\n'  + message['content'] }}\n{% endif %}\n{% if loop.last and add_generation_prompt %}\n{{ '### Assistant:' }}\n{% endif %}\n{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "### User:\nHello, how are you?### Assistant:\nI'm doing great. How can I help you today?### User:\nI'd like to show off how chat templating works!",
            },
            ChatTemplateTestItem {
                name: "Qwen/Qwen1.5-72B-Chat",
                chat_template: "{% for message in messages %}{% if loop.first and messages[0]['role'] != 'system' %}{{ '<|im_start|>system\\nYou are a helpful assistant<|im_end|>\\n' }}{% endif %}{{'<|im_start|>' + message['role'] + '\\n' + message['content']}}{% if (loop.last and add_generation_prompt) or not loop.last %}{{ '<|im_end|>' + '\\n'}}{% endif %}{% endfor %}{% if add_generation_prompt and messages[-1]['role'] != 'assistant' %}{{ '<|im_start|>assistant\\n' }}{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<|im_start|>system\nYou are a helpful assistant<|im_end|>\n<|im_start|>user\nHello, how are you?<|im_end|>\n<|im_start|>assistant\nI'm doing great. How can I help you today?<|im_end|>\n<|im_start|>user\nI'd like to show off how chat templating works!",
            },
            ChatTemplateTestItem {
                name: "deepseek-ai/deepseek-llm-7b-chat",
                chat_template: "{% if not add_generation_prompt is defined %}{% set add_generation_prompt = false %}{% endif %}{{ bos_token }}{% for message in messages %}{% if message['role'] == 'user' %}{{ 'User: ' + message['content'] + '\\n\\n' }}{% elif message['role'] == 'assistant' %}{{ 'Assistant: ' + message['content'] + eos_token }}{% elif message['role'] == 'system' %}{{ message['content'] + '\\n\\n' }}{% endif %}{% endfor %}{% if add_generation_prompt %}{{ 'Assistant:' }}{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<｜begin▁of▁sentence｜>"),
                    eos_token: Some("<｜end▁of▁sentence｜>"),
                    ..Default::default()
                },
                target: "<｜begin▁of▁sentence｜>User: Hello, how are you?\n\nAssistant: I'm doing great. How can I help you today?<｜end▁of▁sentence｜>User: I'd like to show off how chat templating works!\n\n",
            },
            ChatTemplateTestItem {
                name: "h2oai/h2o-danube-1.8b-chat",
                chat_template: "{% for message in messages %}{% if message['role'] == 'user' %}{{ '<|prompt|>' + message['content'] + eos_token }}{% elif message['role'] == 'system' %}{{ '<|system|>' + message['content'] + eos_token }}{% elif message['role'] == 'assistant' %}{{ '<|answer|>'  + message['content'] + eos_token }}{% endif %}{% if loop.last and add_generation_prompt %}{{ '<|answer|>' }}{% endif %}{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<|prompt|>Hello, how are you?</s><|answer|>I'm doing great. How can I help you today?</s><|prompt|>I'd like to show off how chat templating works!</s>",
            },
            ChatTemplateTestItem {
                name: "internlm/internlm2-chat-7b",
                chat_template: "{% if messages[0]['role'] == 'user' or messages[0]['role'] == 'system' %}{{ bos_token }}{% endif %}{% for message in messages %}{{ '<|im_start|>' + message['role'] + '\\n' + message['content'] + '<|im_end|>' + '\\n' }}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\\n' }}{% elif messages[-1]['role'] == 'assistant' %}{{ eos_token }}{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "<s><|im_start|>user\nHello, how are you?<|im_end|>\n<|im_start|>assistant\nI'm doing great. How can I help you today?<|im_end|>\n<|im_start|>user\nI'd like to show off how chat templating works!<|im_end|>\n",
            },
            ChatTemplateTestItem {
                name: "TheBloke/deepseek-coder-33B-instruct-AWQ",
                chat_template: "{%- set found_item = false -%}\n{%- for message in messages -%}\n    {%- if message['role'] == 'system' -%}\n        {%- set found_item = true -%}\n    {%- endif -%}\n{%- endfor -%}\n{%- if not found_item -%}\n{{'You are an AI programming assistant, utilizing the Deepseek Coder model, developed by Deepseek Company, and you only answer questions related to computer science. For politically sensitive questions, security and privacy issues, and other non-computer science questions, you will refuse to answer.\\n'}}\n{%- endif %}\n{%- for message in messages %}\n    {%- if message['role'] == 'system' %}\n{{ message['content'] }}\n    {%- else %}\n        {%- if message['role'] == 'user' %}\n{{'### Instruction:\\n' + message['content'] + '\\n'}}\n        {%- else %}\n{{'### Response:\\n' + message['content'] + '\\n<|EOT|>\\n'}}\n        {%- endif %}\n    {%- endif %}\n{%- endfor %}\n{{'### Response:\\n'}}\n",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<｜begin▁of▁sentence｜>"),
                    eos_token: Some("<|EOT|>"),
                    ..Default::default()
                },
                target: "You are an AI programming assistant, utilizing the Deepseek Coder model, developed by Deepseek Company, and you only answer questions related to computer science. For politically sensitive questions, security and privacy issues, and other non-computer science questions, you will refuse to answer.\n### Instruction:\nHello, how are you?\n### Response:\nI'm doing great. How can I help you today?\n<|EOT|>\n### Instruction:\nI'd like to show off how chat templating works!\n### Response:\n",
            },
            ChatTemplateTestItem {
                name: "ericzzz/falcon-rw-1b-chat",
                // `.strip()` has been replaced with `| trim` in the following template
                chat_template: "{% for message in messages %}{% if loop.index > 1 and loop.previtem['role'] != 'assistant' %}{{ ' ' }}{% endif %}{% if message['role'] == 'system' %}{{ '[SYS] ' + message['content'] | trim }}{% elif message['role'] == 'user' %}{{ '[INST] ' + message['content'] | trim }}{% elif message['role'] == 'assistant' %}{{ '[RESP] '  + message['content'] + eos_token }}{% endif %}{% endfor %}{% if add_generation_prompt %}{{ ' [RESP] ' }}{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<|endoftext|>"),
                    eos_token: Some("<|endoftext|>"),
                    ..Default::default()
                },
                target: "[INST] Hello, how are you? [RESP] I'm doing great. How can I help you today?<|endoftext|>[INST] I'd like to show off how chat templating works!",
            },
            ChatTemplateTestItem {
                name: "abacusai/Smaug-34B-v0.1",
                chat_template: "{%- for idx in range(0, messages|length) -%}\n{%- if messages[idx]['role'] == 'user' -%}\n{%- if idx > 1 -%}\n{{- bos_token + '[INST] ' + messages[idx]['content'] + ' [/INST]' -}}\n{%- else -%}\n{{- messages[idx]['content'] + ' [/INST]' -}}\n{%- endif -%}\n{% elif messages[idx]['role'] == 'system' %}\n{{- '[INST] <<SYS>>\\n' + messages[idx]['content'] + '\\n<</SYS>>\\n\\n' -}}\n{%- elif messages[idx]['role'] == 'assistant' -%}\n{{- ' '  + messages[idx]['content'] + ' ' + eos_token -}}\n{% endif %}\n{% endfor %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "Hello, how are you? [/INST] I'm doing great. How can I help you today? </s><s>[INST] I'd like to show off how chat templating works! [/INST]",
            },
            ChatTemplateTestItem {
                name: "maywell/Synatra-Mixtral-8x7B",
                chat_template: "Below is an instruction that describes a task. Write a response that appropriately completes the request.\n\n{% for message in messages %}{% if message['role'] == 'user' %}### Instruction:\n{{ message['content']|trim -}}{% if not loop.last %}{% endif %}\n{% elif message['role'] == 'assistant' %}### Response:\n{{ message['content']|trim -}}{% if not loop.last %}{% endif %}\n{% elif message['role'] == 'system' %}{{ message['content']|trim -}}{% if not loop.last %}{% endif %}\n{% endif %}\n{% endfor %}\n{% if add_generation_prompt and messages[-1]['role'] != 'assistant' %}\n### Response:\n{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "Below is an instruction that describes a task. Write a response that appropriately completes the request.### Instruction:Hello, how are you?### Response:I'm doing great. How can I help you today?### Instruction:I'd like to show off how chat templating works!",
            },
            ChatTemplateTestItem {
                name: "deepseek-ai/deepseek-coder-33b-instruct",
                chat_template: "{% if not add_generation_prompt is defined %}\n{% set add_generation_prompt = false %}\n{% endif %}\n{%- set ns = namespace(found=false) -%}\n{%- for message in messages -%}\n    {%- if message['role'] == 'system' -%}\n        {%- set ns.found = true -%}\n    {%- endif -%}\n{%- endfor -%}\n{{bos_token}}{%- if not ns.found -%}\n{{'You are an AI programming assistant, utilizing the Deepseek Coder model, developed by Deepseek Company, and you only answer questions related to computer science. For politically sensitive questions, security and privacy issues, and other non-computer science questions, you will refuse to answer\\n'}}\n{%- endif %}\n{%- for message in messages %}\n    {%- if message['role'] == 'system' %}\n{{ message['content'] }}\n    {%- else %}\n        {%- if message['role'] == 'user' %}\n{{'### Instruction:\\n' + message['content'] + '\\n'}}\n        {%- else %}\n{{'### Response:\\n' + message['content'] + '\\n<|EOT|>\\n'}}\n        {%- endif %}\n    {%- endif %}\n{%- endfor %}\n{% if add_generation_prompt %}\n{{'### Response:'}}\n{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<｜begin▁of▁sentence｜>"),
                    eos_token: Some("</EOT>"),
                    ..Default::default()
                },
                target: "<｜begin▁of▁sentence｜>You are an AI programming assistant, utilizing the Deepseek Coder model, developed by Deepseek Company, and you only answer questions related to computer science. For politically sensitive questions, security and privacy issues, and other non-computer science questions, you will refuse to answer\n### Instruction:\nHello, how are you?\n### Response:\nI'm doing great. How can I help you today?\n<|EOT|>\n### Instruction:\nI'd like to show off how chat templating works!\n",
            },
            // NOT INCLUDED
            // - meetkai/functionary-medium-v3.2
            // - fireworks-ai/firefunction-v1
            // https://github
            ChatTemplateTestItem {
                name: "maywell/PiVoT-MoE",
                chat_template: "{{ (messages|selectattr('role', 'equalto', 'system')|list|last).content|trim if (messages|selectattr('role', 'equalto', 'system')|list) else '' }}{% for message in messages %}{% if message['role'] == 'system' %}{{ message['content']|trim }}{% elif message['role'] == 'user' %}### Instruction: {{ message['content']|trim }}{% elif message['role'] == 'assistant' %}### Response: {{ message['content']|trim }}{% elif message['role'] == 'user_context' %}### Input: {{ message['content']|trim }}{% endif %}{% if not loop.last %}\n{% endif %}{% endfor %}{% if add_generation_prompt and messages[-1]['role'] != 'assistant' %}### Response:{% endif %}",
                input: ChatTemplateInputs {
                    messages: example_chat_with_system.clone(),
                    add_generation_prompt: false,
                    bos_token: Some("<s>"),
                    eos_token: Some("</s>"),
                    ..Default::default()
                },
                target: "You are a friendly chatbot who always responds in the style of a pirateYou are a friendly chatbot who always responds in the style of a pirate### Instruction: Hello, how are you?### Response: I'm doing great. How can I help you today?### Instruction: I'd like to show off how chat templating works!",
            },
        ];

        #[allow(unused_variables)] // name is unused
        for ChatTemplateTestItem {
            name,
            chat_template,
            input,
            target,
        } in test_custom_templates
        {
            let mut env = Environment::new();
            env.add_function("raise_exception", raise_exception);
            env.add_function("strftime_now", strftime_now);
            // trim all the whitespace
            let chat_template = chat_template
                .lines()
                .map(|line| line.trim())
                .collect::<Vec<&str>>()
                .join("");

            let tmpl = env.template_from_str(&chat_template);
            let result = tmpl.unwrap().render(input).unwrap();
            assert_eq!(result, target);
        }
    }

    #[test]
    fn test_chat_template_with_custom_tool_template() {
        let mut kwargs = HashMap::new();
        kwargs.insert(
            "bos_token".to_string(),
            serde_json::Value::String("<s>".to_string()),
        );
        kwargs.insert(
            "eos_token".to_string(),
            serde_json::Value::String("</s>".to_string()),
        );

        // chat template from meta-llama/Meta-Llama-3.1-8B-Instruct
        let ct = ChatTemplate::new(
            "{{- bos_token }}\n{%- if not tools_in_user_message is defined %}\n    {%- set tools_in_user_message = true %}\n{%- endif %}\n{%- if not date_string is defined %}\n    {%- set date_string = \"26 Jul 2024\" %}\n{%- endif %}\n{%- if not tools is defined %}\n    {%- set tools = none %}\n{%- endif %}\n\n{#- This block extracts the system message, so we can slot it into the right place. #}\n{%- if messages[0]['role'] == 'system' %}\n    {%- set system_message = messages[0]['content']|trim %}\n    {%- set messages = messages[1:] %}\n{%- else %}\n    {%- set system_message = \"\" %}\n{%- endif %}\n\n{#- System message + builtin tools #}\n{{- \"<|start_header_id|>system<|end_header_id|>\\n\\n\" }}\n{%- if builtin_tools is defined or tools is not none %}\n    {{- \"Environment: ipython\\n\" }}\n{%- endif %}\n{%- if builtin_tools is defined %}\n    {{- \"Tools: \" + builtin_tools | reject('equalto', 'code_interpreter') | join(\", \") + \"\\n\\n\"}}\n{%- endif %}\n{{- \"Cutting Knowledge Date: December 2023\\n\" }}\n{{- \"Today Date: \" + date_string + \"\\n\\n\" }}\n{%- if tools is not none and not tools_in_user_message %}\n    {{- \"You have access to the following functions. To call a function, please respond with JSON for a function call.\" }}\n    {{- 'Respond in the format {\"name\": function name, \"parameters\": dictionary of argument name and its value}.' }}\n    {{- \"Do not use variables.\\n\\n\" }}\n    {%- for t in tools %}\n        {{- t | tojson(indent=4) }}\n        {{- \"\\n\\n\" }}\n    {%- endfor %}\n{%- endif %}\n{{- system_message }}\n{{- \"<|eot_id|>\" }}\n\n{#- Custom tools are passed in a user message with some extra guidance #}\n{%- if tools_in_user_message and not tools is none %}\n    {#- Extract the first user message so we can plug it in here #}\n    {%- if messages | length != 0 %}\n        {%- set first_user_message = messages[0]['content']|trim %}\n        {%- set messages = messages[1:] %}\n    {%- else %}\n        {{- raise_exception(\"Cannot put tools in the first user message when there's no first user message!\") }}\n{%- endif %}\n    {{- '<|start_header_id|>user<|end_header_id|>\\n\\n' -}}\n    {{- \"Given the following functions, please respond with a JSON for a function call \" }}\n    {{- \"with its proper arguments that best answers the given prompt.\\n\\n\" }}\n    {{- 'Respond in the format {\"name\": function name, \"parameters\": dictionary of argument name and its value}.' }}\n    {{- \"Do not use variables.\\n\\n\" }}\n    {%- for t in tools %}\n        {{- t | tojson(indent=4) }}\n        {{- \"\\n\\n\" }}\n    {%- endfor %}\n    {{- first_user_message + \"<|eot_id|>\"}}\n{%- endif %}\n\n{%- for message in messages %}\n    {%- if not (message.role == 'ipython' or message.role == 'tool' or 'tool_calls' in message) %}\n        {{- '<|start_header_id|>' + message['role'] + '<|end_header_id|>\\n\\n'+ message['content'] | trim + '<|eot_id|>' }}\n    {%- elif 'tool_calls' in message %}\n        {%- if not message.tool_calls|length == 1 %}\n            {{- raise_exception(\"This model only supports single tool-calls at once!\") }}\n        {%- endif %}\n        {%- set tool_call = message.tool_calls[0].function %}\n        {%- if builtin_tools is defined and tool_call.name in builtin_tools %}\n            {{- '<|start_header_id|>assistant<|end_header_id|>\\n\\n' -}}\n            {{- \"<|python_tag|>\" + tool_call.name + \".call(\" }}\n            {%- for arg_name, arg_val in tool_call.arguments | items %}\n                {{- arg_name + '=\"' + arg_val + '\"' }}\n                {%- if not loop.last %}\n                    {{- \", \" }}\n                {%- endif %}\n                {%- endfor %}\n            {{- \")\" }}\n        {%- else  %}\n            {{- '<|start_header_id|>assistant<|end_header_id|>\\n\\n' -}}\n            {{- '{\"name\": \"' + tool_call.name + '\", ' }}\n            {{- '\"parameters\": ' }}\n            {{- tool_call.arguments | tojson }}\n            {{- \"}\" }}\n        {%- endif %}\n        {%- if builtin_tools is defined %}\n            {#- This means we're in ipython mode #}\n            {{- \"<|eom_id|>\" }}\n        {%- else %}\n            {{- \"<|eot_id|>\" }}\n        {%- endif %}\n    {%- elif message.role == \"tool\" or message.role == \"ipython\" %}\n        {{- \"<|start_header_id|>ipython<|end_header_id|>\\n\\n\" }}\n        {%- if message.content is mapping or message.content is iterable %}\n            {{- message.content | tojson }}\n        {%- else %}\n            {{- message.content }}\n        {%- endif %}\n        {{- \"<|eot_id|>\" }}\n    {%- endif %}\n{%- endfor %}\n{%- if add_generation_prompt %}\n    {{- '<|start_header_id|>assistant<|end_header_id|>\\n\\n' }}\n{%- endif %}\n".to_string(),
            Some(kwargs),
        );
        let msgs: Vec<Message> = vec![
            Message {
                name: None,
                role: "system".to_string(),
                body: MessageBody::Content {
                    content: MessageContent::SingleText(
                        "Youre a helpful assistant! Answer the users question best you can."
                            .to_string(),
                    ),
                },
            },
            Message {
                name: None,
                role: "user".to_string(),
                body: MessageBody::Content {
                    content: MessageContent::SingleText(
                        "What is the weather like in Brooklyn, New York?".to_string(),
                    ),
                },
            },
        ];
        let tools_string = r#"[{"type": "function","function": {"name": "get_current_weather","description": "Get the current weather","parameters": {"type": "object","properties": {"location": {"type": "string","description": "The city and state, e.g. San Francisco, CA"},"format": {"type": "string","enum": ["celsius", "fahrenheit"],"description": "The temperature unit to use. Infer this from the users location."}},"required": ["location", "format"]}}}]"#.to_string();
        let tools: Vec<Tool> = serde_json::from_str(&tools_string).unwrap();

        // Convert tools to JSON values for template processing
        let tools_json: Option<Vec<serde_json::Value>> = Some(tools)
            .as_ref()
            .map(|tools| {
                tools
                    .iter()
                    .map(serde_json::to_value)
                    .collect::<Result<Vec<_>, _>>()
            })
            .transpose()
            .map_err(|e| format!("Failed to serialize tools: {}", e))
            .unwrap();

        let params = ChatTemplateParams {
            tools: tools_json.as_deref(),
            add_generation_prompt: true,
            template_kwargs: None,
        };

        let messages = msgs
            .iter()
            .map(|m| serde_json::to_value(m).unwrap())
            .collect::<Vec<serde_json::Value>>();

        let result = ct.apply(&messages, params);
        let expected = r#"<s><|start_header_id|>system<|end_header_id|>

Environment: ipython
Cutting Knowledge Date: December 2023
Today Date: 26 Jul 2024

Youre a helpful assistant! Answer the users question best you can.<|eot_id|><|start_header_id|>user<|end_header_id|>

Given the following functions, please respond with a JSON for a function call with its proper arguments that best answers the given prompt.

Respond in the format {"name": function name, "parameters": dictionary of argument name and its value}.Do not use variables.

{
    "function": {
        "description": "Get the current weather",
        "name": "get_current_weather",
        "parameters": {
            "properties": {
                "format": {
                    "description": "The temperature unit to use. Infer this from the users location.",
                    "enum": [
                        "celsius",
                        "fahrenheit"
                    ],
                    "type": "string"
                },
                "location": {
                    "description": "The city and state, e.g. San Francisco, CA",
                    "type": "string"
                }
            },
            "required": [
                "location",
                "format"
            ],
            "type": "object"
        }
    },
    "type": "function"
}

What is the weather like in Brooklyn, New York?<|eot_id|><|start_header_id|>assistant<|end_header_id|>

"#;
        assert_eq!(result.unwrap(), expected);
    }

    #[test]
    fn test_chat_template_with_special_system_prompt() {
        let mut kwargs = HashMap::new();
        kwargs.insert(
            "bos_token".to_string(),
            serde_json::Value::String("<bos>".to_string()),
        );
        kwargs.insert(
            "eos_token".to_string(),
            serde_json::Value::String("</eos>".to_string()),
        );

        // chat template from gemma3
        let ct = ChatTemplate::new(
            r#"{{ bos_token }}
{%- if messages[0]['role'] == 'system' -%}
    {%- set first_user_prefix = messages[0]['content'][0]['text'] + '

' -%}
    {%- set loop_messages = messages[1:] -%}
{%- else -%}
    {%- set first_user_prefix = "" -%}
    {%- set loop_messages = messages -%}
{%- endif -%}
{%- for message in loop_messages -%}
    {%- if (message['role'] == 'user') != (loop.index0 % 2 == 0) -%}
        {{ raise_exception("Conversation roles must alternate user/assistant/user/assistant/...") }}
    {%- endif -%}
    {%- if (message['role'] == 'assistant') -%}
        {%- set role = "model" -%}
    {%- else -%}
        {%- set role = message['role'] -%}
    {%- endif -%}
    {{ '<start_of_turn>' + role + '
' + (first_user_prefix if loop.first else "") }}
    {%- if message['content'] is string -%}
        {{ message['content'] | trim }}
    {%- elif message['content'] is iterable -%}
        {%- for item in message['content'] -%}
            {%- if item['type'] == 'image' -%}
                {{ '<start_of_image>' }}
            {%- elif item['type'] == 'text' -%}
                {{ item['text'] | trim }}
            {%- endif -%}
        {%- endfor -%}
    {%- else -%}
        {{ raise_exception("Invalid content type") }}
    {%- endif -%}
    {{ '<end_of_turn>
' }}
{%- endfor -%}
{%- if add_generation_prompt -%}
    {{'<start_of_turn>model
'}}
{%- endif -%}
"#
            .to_string(),
            Some(kwargs),
        );
        let msgs: Vec<Message> = vec![
            Message {
                name: None,
                role: "system".to_string(),
                body: MessageBody::Content {
                    content: MessageContent::MultipleChunks(vec![MessageChunk::Text {
                        text: "You are a helpful assistant.".to_string(),
                    }]),
                },
            },
            Message {
                name: None,
                role: "user".to_string(),
                body: MessageBody::Content {
                    content: MessageContent::MultipleChunks(vec![
                        MessageChunk::Text {
                            text: "I'm already using this supplement ".to_string(),
                        },
                        MessageChunk::ImageUrl {
                            image_url: Url {
                                url:  "https://huggingface.co/datasets/merve/vlm_test_images/resolve/main/IMG_3018.JPG".to_string()
                            },
                        },
                        MessageChunk::Text {
                            text: "and I want to use this one too ".to_string()
                        },
                        MessageChunk::ImageUrl {
                            image_url: Url {
                                url: "https://huggingface.co/datasets/merve/vlm_test_images/resolve/main/IMG_3015.jpg".to_string()
                            },
                        },
                        MessageChunk::Text {
                            text: " what are cautions?".to_string()
                        },
                    ]),
                },
            },
        ];

        let params = ChatTemplateParams {
            tools: Some(&vec![]),
            add_generation_prompt: true,
            template_kwargs: None,
        };
        let messages: Vec<TextMessage> = msgs.into_iter().map(|c| c.into()).collect();
        let messages = messages
            .into_iter()
            .map(|m| serde_json::to_value(m).unwrap())
            .collect::<Vec<serde_json::Value>>();
        let result = ct.apply(&messages, params);
        let expected = "<bos><start_of_turn>user\nYou are a helpful assistant.\n\nI'm already using this supplement ![](https://huggingface.co/datasets/merve/vlm_test_images/resolve/main/IMG_3018.JPG)and I want to use this one too ![](https://huggingface.co/datasets/merve/vlm_test_images/resolve/main/IMG_3015.jpg) what are cautions?<end_of_turn>\n<start_of_turn>model\n".to_string();
        assert_eq!(result.unwrap(), expected);
    }

    #[test]
    fn test_qwen_coder_template() {
        let config_path = PathBuf::from(
            "test/data/Qwen/Qwen3-Coder-480B-A35B-Instruct-FP8/tokenizer_config.json",
        );

        let content = std::fs::read_to_string(config_path).unwrap();

        let config = serde_json::from_str::<TokenizerConfig>(&content).unwrap();

        let chat_template_str = config.chat_template.unwrap().to_string();

        let template_kwargs = {
            let mut m = HashMap::new();
            if let Some(bos_token) = &config.bos_token {
                m.insert(
                    "bos_token".to_string(),
                    serde_json::Value::String(bos_token.clone().as_str().to_string()),
                );
            }
            if let Some(eos_token) = &config.eos_token {
                m.insert(
                    "eos_token".to_string(),
                    serde_json::Value::String(eos_token.clone().as_str().to_string()),
                );
            }
            m
        };
        let chat_template = ChatTemplate::new(chat_template_str, Some(template_kwargs));

        let messages = r#"
        [
            {"role": "system", "content": "You are a helpful assistant."},
            {"role": "assistant", "content": "Hello how can I help?"},
            {"role": "user", "content": "What is Deep Learning?"},
            {"role": "assistant", "content": "magic!"}
        ]
        "#;

        let messages: Vec<Message> = serde_json::from_str(messages).unwrap();

        let messages = messages
            .iter()
            .map(|m| serde_json::to_value(m).unwrap())
            .collect::<Vec<serde_json::Value>>();

        let params = ChatTemplateParams {
            tools: None,
            add_generation_prompt: true,
            template_kwargs: None,
        };

        let result = chat_template.apply(&messages, params).unwrap();

        assert_eq!(
            result,
            "<|im_start|>system\nYou are a helpful assistant.<|im_end|>\n<|im_start|>assistant\nHello how can I help?<|im_end|>\n<|im_start|>user\nWhat is Deep Learning?<|im_end|>\n<|im_start|>assistant\nmagic!<|im_end|>\n<|im_start|>assistant\n".to_string()
        );
    }
}
