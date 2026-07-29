package discovery

import "testing"

func TestParseARN(t *testing.T) {
	cases := []struct {
		name string
		arn  string
		want ARN
	}{
		{
			name: "bare colon-form -- SQS queue, this session's own real probe",
			arn:  "arn:aws:sqs:us-east-1:839333509514:ubx-discovery-1785357707",
			want: ARN{
				Partition: "aws", Service: "sqs", Region: "us-east-1", Account: "839333509514",
				Resource: "ubx-discovery-1785357707", ResourceTypePrefix: "", ResourceID: "ubx-discovery-1785357707",
			},
		},
		{
			name: "type/id form -- IAM role",
			arn:  "arn:aws:iam::839333509514:role/my-role",
			want: ARN{
				Partition: "aws", Service: "iam", Region: "", Account: "839333509514",
				Resource: "role/my-role", ResourceTypePrefix: "role", ResourceID: "my-role",
			},
		},
		{
			name: "type/id form -- VPC",
			arn:  "arn:aws:ec2:us-east-1:839333509514:vpc/vpc-b75be9cd",
			want: ARN{
				Partition: "aws", Service: "ec2", Region: "us-east-1", Account: "839333509514",
				Resource: "vpc/vpc-b75be9cd", ResourceTypePrefix: "vpc", ResourceID: "vpc-b75be9cd",
			},
		},
		{
			name: "bare colon-form, no account/region -- S3 bucket",
			arn:  "arn:aws:s3:::my-bucket",
			want: ARN{
				Partition: "aws", Service: "s3", Region: "", Account: "",
				Resource: "my-bucket", ResourceTypePrefix: "", ResourceID: "my-bucket",
			},
		},
		{
			name: "nested path -- multiple slashes",
			arn:  "arn:aws:s3:::my-bucket/path/to/key",
			want: ARN{
				Partition: "aws", Service: "s3", Region: "", Account: "",
				Resource: "my-bucket/path/to/key", ResourceTypePrefix: "my-bucket", ResourceID: "path/to/key",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseARN(c.arn)
			if err != nil {
				t.Fatalf("ParseARN(%q): %v", c.arn, err)
			}
			if got != c.want {
				t.Fatalf("ParseARN(%q) = %+v, want %+v", c.arn, got, c.want)
			}
		})
	}
}

func TestParseARN_Malformed(t *testing.T) {
	cases := []string{
		"",
		"not-an-arn",
		"arn:aws:sqs:us-east-1", // too few fields
		"urn:aws:sqs:us-east-1:acct:thing",
	}
	for _, arn := range cases {
		if _, err := ParseARN(arn); err == nil {
			t.Fatalf("ParseARN(%q): expected an error, got nil", arn)
		}
	}
}

func TestARN_ClassKey(t *testing.T) {
	cases := []struct {
		arn  string
		want string
	}{
		{"arn:aws:sqs:us-east-1:839333509514:ubx-discovery-1785357707", "sqs:"},
		{"arn:aws:iam::839333509514:role/my-role", "iam:role"},
		{"arn:aws:iam::839333509514:user/my-user", "iam:user"},
		{"arn:aws:iam::839333509514:policy/my-policy", "iam:policy"},
	}
	for _, c := range cases {
		a, err := ParseARN(c.arn)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.classKey(); got != c.want {
			t.Fatalf("classKey(%q) = %q, want %q", c.arn, got, c.want)
		}
	}
}
