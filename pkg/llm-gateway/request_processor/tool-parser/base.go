package tool_parser

type ToolParser interface {
	ParseComplete(text string) (*ParseResult, error)
	ParseStreaming(chunk string) (*ParseResult, error)
}

func CreateToolParser(name string) ToolParser {
	switch name {
	case "deepseek_v3":
		return NewDeepSeekParser()
	case "deepseek_v31":
		return NewDeepSeekV31Parser()
	}
	return nil
}
