use serde::{Deserialize, Serialize};
use serde_json::Value;
use utoipa::ToSchema;

fn serialize_as_string<S>(value: &serde_json::Value, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    serializer.serialize_str(&value.to_string())
}

// #[derive(Clone, Deserialize, Serialize, ToSchema, Debug, PartialEq)]
#[derive(Clone, Deserialize, Serialize, ToSchema, Debug, PartialEq)]
#[serde(untagged)]
pub enum MessageContent {
    SingleText(String),
    MultipleChunks(Vec<MessageChunk>),
}

#[derive(Clone, Deserialize, ToSchema, Serialize, Debug, PartialEq)]
pub struct Url {
    pub url: String,
}

#[derive(Clone, Deserialize, ToSchema, Serialize, Debug, PartialEq)]
#[serde(tag = "type")]
#[serde(rename_all = "snake_case")]
pub enum MessageChunk {
    Text { text: String },
    ImageUrl { image_url: Url },
}

// Pushing a chunk to a single text message will convert it to a multiple chunks message
impl MessageContent {
    pub fn push(&mut self, chunk: MessageChunk) {
        match self {
            MessageContent::SingleText(text) => {
                *self = MessageContent::MultipleChunks(vec![
                    MessageChunk::Text { text: text.clone() },
                    chunk,
                ]);
            }
            MessageContent::MultipleChunks(chunks) => {
                chunks.push(chunk);
            }
        }
    }
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct Tool {
    // The type of the tool. Currently, only 'function' is supported.
    #[serde(rename = "type")]
    pub tool_type: String, // "function"
    pub function: Function,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct Function {
    pub name: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
    pub parameters: Value, // JSON Schema
    /// Whether to enable strict schema adherence (OpenAI structured outputs)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub strict: Option<bool>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, ToSchema)]
pub struct ToolCall {
    pub id: String,
    #[serde(rename = "type")]
    pub tool_type: String, // "function"
    pub function: FunctionCallResponse,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(untagged)]
pub enum FunctionCall {
    None,
    Auto,
    Function { name: String },
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, ToSchema)]
pub struct FunctionCallResponse {
    pub name: String,
    #[serde(default)]
    pub arguments: Option<String>, // JSON string
}

#[derive(Clone, Deserialize, ToSchema, Serialize, Debug, PartialEq, Default)]
pub struct TextMessage {
    #[schema(example = "user")]
    pub role: String,
    #[schema(example = "My name is David and I")]
    pub content: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool_call_id: Option<String>,
}

impl From<Message> for TextMessage {
    fn from(value: Message) -> Self {
        let content = match value.body {
            MessageBody::Content { content } => content,
            MessageBody::Tool { tool_calls } => {
                let content = serde_json::to_string(&tool_calls).unwrap_or_default();
                MessageContent::SingleText(content)
            }
        };
        TextMessage {
            role: value.role,
            content: match content {
                MessageContent::SingleText(text) => text,
                MessageContent::MultipleChunks(chunks) => chunks
                    .into_iter()
                    .map(|chunk| match chunk {
                        MessageChunk::Text { text } => text,
                        MessageChunk::ImageUrl { image_url } => format!("![]({})", image_url.url),
                    })
                    .collect::<Vec<_>>()
                    .join(""),
            },
            ..Default::default()
        }
    }
}

#[derive(Clone, Deserialize, Serialize, ToSchema, Debug, PartialEq)]
pub struct Message {
    #[schema(example = "user")]
    pub role: String,
    #[serde(flatten)]
    #[schema(example = "My name is David and I")]
    pub body: MessageBody,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[schema(example = "\"David\"")]
    pub name: Option<String>,
}

#[derive(Clone, Deserialize, Serialize, ToSchema, Debug, PartialEq)]
#[serde(untagged)]
pub enum MessageBody {
    // When a regular text message is provided.
    Content {
        #[serde(rename = "content")]
        content: MessageContent,
    },
    // When tool calls are provided.
    Tool {
        #[serde(rename = "tool_calls")]
        tool_calls: Vec<ToolCall>,
    },
}
