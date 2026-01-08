package main

import (
	"fmt"
	"log"
	"tokenizers"
)

func simple() error {
	fmt.Println("=== Simple Example ===")

	tk, err := tokenizers.FromFile("../test/data/bert-base-uncased.json")
	if err != nil {
		return fmt.Errorf("failed to load tokenizer: %w", err)
	}
	defer tk.Close()

	// Display vocab size
	fmt.Printf("Vocab size: %d\n", tk.VocabSize())

	// Display model max length
	fmt.Printf("Model max length: %d\n", tk.MaxModelLen())

	text := "brown fox jumps over the lazy dog"

	// Encode without special tokens
	ids := tk.Encode(text, false)
	fmt.Printf("Encode (without special tokens): %v\n", ids)

	// Encode with special tokens
	idsWithSpecial := tk.Encode(text, true)
	fmt.Printf("Encode (with special tokens): %v\n", idsWithSpecial)

	// Decode
	decoded := tk.Decode([]uint32{2829, 4419, 14523, 2058, 1996, 13971, 3899}, false)
	fmt.Printf("Decode: %s\n", decoded)

	// Decode with skip special tokens
	decodedSkip := tk.Decode(idsWithSpecial, true)
	fmt.Printf("Decode (skip special tokens): %s\n\n", decodedSkip)

	return nil
}

func chatTemplate() error {
	fmt.Println("=== Chat Template Example ===")

	// Note: You need a tokenizer with chat template support
	tk, err := tokenizers.FromFileWithChatTemplate(
		"../test/data/Qwen/Qwen3-8B",
		"", // chat template path (if separate file)
	)
	if err != nil {
		return fmt.Errorf("failed to load tokenizer with chat template: %w", err)
	}
	defer tk.Close()

	// Example messages in JSON format
	messages := `[
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "Hello, how are you?"}
    ]`

	// Apply chat template
	formatted, err := tk.ApplyChatTemplate(messages, "", "")
	if err != nil {
		// Chat template might not be supported for this tokenizer
		fmt.Printf("Chat template not available: %v\n\n", err)
		return nil
	}

	fmt.Printf("Formatted chat: %s\n", formatted)

	// Encode the formatted chat
	ids := tk.Encode(formatted, true)
	fmt.Printf("Token IDs: %v\n\n", ids)

	return nil
}

func multipleTexts() error {
	fmt.Println("=== Multiple Texts Example ===")

	tk, err := tokenizers.FromFile("../test/data/Qwen/Qwen3-8B")
	if err != nil {
		return fmt.Errorf("failed to load tokenizer: %w", err)
	}
	defer tk.Close()

	texts := []string{
		"First sentence.",
		"Second sentence is longer.",
		"Third one is the longest sentence here.",
	}

	for i, text := range texts {
		ids := tk.Encode(text, true)
		decoded := tk.Decode(ids, true)
		fmt.Printf("Text %d: %s\n", i+1, text)
		fmt.Printf("  IDs: %v\n", ids)
		fmt.Printf("  Decoded: %s\n", decoded)
	}
	fmt.Println()

	return nil
}

func edgeCases() error {
	fmt.Println("=== Edge Cases Example ===")

	tk, err := tokenizers.FromFile("../test/data/Qwen/Qwen3-8B")
	if err != nil {
		return fmt.Errorf("failed to load tokenizer: %w", err)
	}
	defer tk.Close()

	// Empty string
	emptyIds := tk.Encode("", false)
	fmt.Printf("Empty string IDs: %v\n", emptyIds)

	// Special characters
	special := "!@#$%^&*()"
	specialIds := tk.Encode(special, false)
	fmt.Printf("Special chars '%s' IDs: %v\n", special, specialIds)

	// Numbers
	numbers := "123456789"
	numberIds := tk.Encode(numbers, false)
	fmt.Printf("Numbers '%s' IDs: %v\n", numbers, numberIds)

	// Mixed languages (if supported)
	mixed := "Hello 世界"
	mixedIds := tk.Encode(mixed, false)
	fmt.Printf("Mixed text '%s' IDs: %v\n", mixed, mixedIds)
	decoded := tk.Decode(mixedIds, false)
	fmt.Printf("Decoded: %s\n\n", decoded)

	return nil
}

func main() {
	examples := []struct {
		name string
		fn   func() error
	}{
		{"Simple", simple},
		{"Chat Template", chatTemplate},
		{"Multiple Texts", multipleTexts},
		{"Edge Cases", edgeCases},
	}

	for _, example := range examples {
		fmt.Printf("Running %s...\n", example.name)
		if err := example.fn(); err != nil {
			log.Printf("Error in %s: %v\n", example.name, err)
			// Continue to next example instead of fatal exit
			continue
		}
	}

	fmt.Println("All examples completed!")
}
