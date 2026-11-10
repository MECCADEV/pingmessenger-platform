package channels

import "testing"

func TestValidateSearchPage(t *testing.T) {
	for _, test := range []struct {
		limit, offset int
		valid         bool
	}{
		{limit: 20, offset: 0, valid: true},
		{limit: 1, offset: 10000, valid: true},
		{limit: 0, offset: 0},
		{limit: 51, offset: 0},
		{limit: 20, offset: -1},
		{limit: 20, offset: 10001},
	} {
		err := ValidateSearchPage(test.limit, test.offset)
		if test.valid && err != nil {
			t.Errorf("ValidateSearchPage(%d,%d) = %v", test.limit, test.offset, err)
		}
		if !test.valid && err == nil {
			t.Errorf("ValidateSearchPage(%d,%d) unexpectedly succeeded", test.limit, test.offset)
		}
	}
}

func TestEscapeLikeTreatsWildcardsLiterally(t *testing.T) {
	if got := escapeLike(`100%_\safe`); got != `100\%\_\\safe` {
		t.Fatalf("escapeLike() = %q", got)
	}
}
