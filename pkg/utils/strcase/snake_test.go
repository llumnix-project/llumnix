package strcase

import (
	"testing"
)

func TestToSnake(t *testing.T) {
	// Test case 1: Input a normal string and verify if the output string is converted to snake_case format
	input1 := "helloWorld"
	expected1 := "hello_world"
	output1 := ToSnake(input1)
	if output1 != expected1 {
		t.Errorf("ToSnake(%s) = %s; expected %s", input1, output1, expected1)
	}

	// Test case 2: Input a string that contains uppercase letters and numbers, and verify if the output string is converted to snake_case format
	input2 := "Hello123World"
	expected2 := "hello_123_world"
	output2 := ToSnake(input2)
	if output2 != expected2 {
		t.Errorf("ToSnake(%s) = %s; expected %s", input2, output2, expected2)
	}

	// Test case 3: Input a string that contains special characters, and verify if the output string is converted to snake_case format
	input3 := "Hello-World"
	expected3 := "hello_world"
	output3 := ToSnake(input3)
	if output3 != expected3 {
		t.Errorf("ToSnake(%s) = %s; expected %s", input3, output3, expected3)
	}

	// Test case 4: Input an empty string and verify if the output string is empty
	input4 := ""
	expected4 := ""
	output4 := ToSnake(input4)
	if output4 != expected4 {
		t.Errorf("ToSnake(%s) = %s; expected %s", input4, output4, expected4)
	}
}
