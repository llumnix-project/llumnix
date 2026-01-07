import openai
import json

client = openai.OpenAI(
    api_key="ZDcxNGY2NDU2YTE4ZThkMWZmMGM2MzhhNDg0NWIwZTg1NWVmNTk1Mg==",
    base_url=
    "http://1748815774914563.cn-beijing.pai-eas.aliyuncs.com/api/predict/group_deepseek_gw.deepseek_gw/v1"
)

tools = [{
    "type": "function",
    "function": {
        "name": "get_weather",
        "description": "Get current weather information for a location",
        "parameters": {
            "type": "object",
            "properties": {
                "location": {
                    "type": "string",
                    "description": "The city and state, e.g. San Francisco, CA"
                },
                "unit": {
                    "type": "string",
                    "enum": ["celsius", "fahrenheit"],
                    "description": "The unit of temperature to use"
                }
            },
            "required": ["location"]
        }
    }
}, {
    "type": "function",
    "function": {
        "name": "web_search",
        "description": "search the web for relevant information",
        "parameters": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "search query content"
                }
            }
        }
    }
}]

while True:
    messages = [{
        "role":
        "user",
        "content":
        "What is the weather like in New York? and at the same time search the web for the latest news about AI?"
    }]

    stream = client.chat.completions.create(model="DeepSeek-R1-0528",
                                            messages=messages,
                                            tools=tools,
                                            stream=False,
                                            max_completion_tokens = 64000)

    # print("\n✅ 1full content:\n", stream.choices[0].message.content)
    full_tool_calls = stream.choices[0].message.tool_calls
    print("\n🔍 " + stream.id + " 1calls:")
    for tc in full_tool_calls:
        name = tc.function.name  # ["function"]["name"]
        args_str = tc.function.arguments  #tc["function"]["arguments"]
        try:
            args = json.loads(args_str)
            print(f" function: {name}, args: {args}")
        except json.JSONDecodeError as e:
            print(f"  ❌ 1 parse error: {args_str} ({e})")
    if len(full_tool_calls) == 0:
        print("\n✅ 1 no tools called\n", stream.choices[0].message.content)
        print("\n usage: ", stream.usage)
        print("\n-----------------------\n")

    stream = client.chat.completions.create(model="DeepSeek-R1-0528",
                                            messages=messages,
                                            tools=tools,
                                            stream=True,
                                            stream_options={
                                                "include_usage": True,
                                                "continuous_usage_stats": True
                                            },
                                            max_completion_tokens = 64000)

    full_tool_calls = []
    full_content = ""
    last_chunk = None

    for chunk in stream:
        last_chunk = chunk
        if chunk.choices is None or len(chunk.choices) == 0:
            continue
        #print("\n--- chunk received ---\n ", chunk)
        delta = chunk.choices[0].delta

        # if delta.reasoning_content:
        #     print("\n🤔 reasoning:", delta.reasoning_content)

        # # handle content
        if delta.content:
            full_content += delta.content
        #     print(delta.content, end="", flush=True)  # 实时输出文本

        # handle tool_calls
        if delta.tool_calls:
            for tool_delta in delta.tool_calls:
                #print("\n\n🔍 tool call delta:", tool_delta)
                index = tool_delta.index

                while len(full_tool_calls) <= index:
                    full_tool_calls.append({"function": {"arguments": ""}})

                if tool_delta.id is not None:
                    full_tool_calls[index]["id"] = tool_delta.id
                if tool_delta.type is not None:
                    full_tool_calls[index]["type"] = tool_delta.type
                if tool_delta.function:
                    if tool_delta.function.name is not None:
                        full_tool_calls[index]["function"][
                            "name"] = tool_delta.function.name
                    if tool_delta.function.arguments is not None:
                        full_tool_calls[index]["function"][
                            "arguments"] += tool_delta.function.arguments

    #print("\n✅ 2full content:\n", full_content)

    print("\n🔍 " + chunk.id + " 2calls:")
    if full_tool_calls:
        for tc in full_tool_calls:
            name = tc["function"]["name"]
            args_str = tc["function"]["arguments"]
            try:
                args = json.loads(args_str)
                print(f" function: {name}, args: {args}")
            except json.JSONDecodeError as e:
                print(f"  ❌ 2parse error: {args_str} ({e})")
                exit(1)
    else:
        print("\n✅ 2no tools called.", full_content)
        print("\n usage: ", last_chunk.usage)
        print("\n-----------------------\n")
