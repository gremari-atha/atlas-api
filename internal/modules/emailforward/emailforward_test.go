package emailforward

import (
	"testing"
)

func TestEmailParser_SanitizeEmail(t *testing.T) {
	parser := NewEmailParser()
	tests := []struct {
		input    string
		expected string
	}{
		{"user.e2e.netflix@gmail.com", "user_e2e_netflix_gmail_com"},
		{"My.Name@Some-Domain.Co.Id", "my_name_some-domain_co_id"},
		{"abc.def@gmail.com", "abc_def_gmail_com"},
	}

	for _, tt := range tests {
		actual := parser.SanitizeEmail(tt.input)
		if actual != tt.expected {
			t.Errorf("SanitizeEmail(%q) = %q; want %q", tt.input, actual, tt.expected)
		}
	}
}

func TestEmailParser_ExtractOtp(t *testing.T) {
	parser := NewEmailParser()
	tests := []struct {
		input    string
		expected *string
	}{
		{
			input:    "Your Netflix verification code is:\n884729\nIt will expire in 10 minutes.",
			expected: stringPtr("884729"),
		},
		{
			input:    "   123456   ",
			expected: stringPtr("123456"),
		},
		{
			input:    "Code is 1234 but there's other text on the line.",
			expected: nil,
		},
		{
			input:    "Your code is:\n  99281  \nExpired.",
			expected: stringPtr("99281"),
		},
		{
			input:    "123\n4567890\n9944",
			expected: stringPtr("9944"), // 4567890 is too long (7 digits), 9944 is 4 digits and on its own line
		},
	}

	for _, tt := range tests {
		actual := parser.ExtractOtp(tt.input)
		if (actual == nil && tt.expected != nil) || (actual != nil && tt.expected == nil) {
			t.Errorf("ExtractOtp(%q) = %v; want %v", tt.input, actual, tt.expected)
		} else if actual != nil && tt.expected != nil && *actual != *tt.expected {
			t.Errorf("ExtractOtp(%q) = %q; want %q", tt.input, *actual, *tt.expected)
		}
	}
}

func TestEmailParser_ExtractNetflixUrl(t *testing.T) {
	parser := NewEmailParser()
	tests := []struct {
		input    string
		expected *string
	}{
		{
			input:    "Please click here to update your password: https://www.netflix.com/password?reset=102834098234908 Thank you.",
			expected: stringPtr("https://www.netflix.com/password?reset=102834098234908"),
		},
		{
			input:    "Update primary location here: https://www.netflix.com/account/update-primary-location?id=99281",
			expected: stringPtr("https://www.netflix.com/account/update-primary-location?id=99281"),
		},
		{
			input:    "Travel verify: https://www.netflix.com/account/travel/verify?token=abc >",
			expected: stringPtr("https://www.netflix.com/account/travel/verify?token=abc"),
		},
		{
			input:    "Netflix verify email: https://www.netflix.com/verifyemail?code=123",
			expected: stringPtr("https://www.netflix.com/verifyemail?code=123"),
		},
		{
			input:    "This link is not netflix: https://www.google.com/password",
			expected: nil,
		},
	}

	for _, tt := range tests {
		actual := parser.ExtractNetflixUrl(tt.input)
		if (actual == nil && tt.expected != nil) || (actual != nil && tt.expected == nil) {
			t.Errorf("ExtractNetflixUrl(%q) = %v; want %v", tt.input, actual, tt.expected)
		} else if actual != nil && tt.expected != nil && *actual != *tt.expected {
			t.Errorf("ExtractNetflixUrl(%q) = %q; want %q", tt.input, *actual, *tt.expected)
		}
	}
}

func stringPtr(s string) *string {
	return &s
}
