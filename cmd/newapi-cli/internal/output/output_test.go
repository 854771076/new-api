package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func render(t *testing.T, raw string) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, Pretty(&buf, json.RawMessage(raw)))
	return buf.String()
}

func TestPretty(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "long string cell truncated to 48 runes",
			raw:  `[{"id":1,"name":"` + strings.Repeat("a", 60) + `"}]`,
			want: "id  name" + strings.Repeat(" ", 45) + "\n" +
				"1   " + strings.Repeat("a", 48) + "…\n",
		},
		{
			name: "cell at rune limit is not truncated",
			raw:  `[{"v":"` + strings.Repeat("c", 48) + `"}]`,
			want: "v" + strings.Repeat(" ", 47) + "\n" +
				strings.Repeat("c", 48) + "\n",
		},
		{
			name: "long multibyte cell truncated on rune boundary",
			raw:  `[{"v":"` + strings.Repeat("中", 60) + `"}]`,
			want: "v" + strings.Repeat(" ", 96) + "\n" +
				strings.Repeat("中", 48) + "…\n",
		},
		{
			name: "nested object cell is truncated compact json",
			raw:  `[{"id":"x","meta":{"description":"` + strings.Repeat("b", 60) + `"}}]`,
			want: "id  meta" + strings.Repeat(" ", 45) + "\n" +
				"x   " + `{"description":"` + strings.Repeat("b", 32) + "…\n",
		},
		{
			name: "chinese cells align by display width",
			raw:  `[{"名称":"测试","状态":"正常"},{"名称":"中文内容测试","状态":"好"}]`,
			want: "名称" + strings.Repeat(" ", 8) + "  " + "状态\n" +
				"测试" + strings.Repeat(" ", 8) + "  " + "正常\n" +
				"中文内容测试" + "  " + "好  \n",
		},
		{
			name: "pagination envelope prints table then totals",
			raw:  `{"items":[{"id":1}],"total":1,"page":2,"page_size":20}`,
			want: "id\n1 \ntotal: 1\npage: 2\npage_size: 20\n",
		},
		{
			name: "empty items envelope",
			raw:  `{"items":[],"total":0,"page":1,"page_size":20}`,
			want: "(0 items)\ntotal: 0\npage: 1\npage_size: 20\n",
		},
		{
			name: "plain object sorted key value",
			raw:  `{"name":"x","id":1,"nested":{"a":1}}`,
			want: "id: 1\nname: x\nnested: {\"a\":1}\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := render(t, tc.raw)
			assert.Equal(t, tc.want, got)
			assert.True(t, utf8.ValidString(got), "output must stay valid UTF-8")
		})
	}
}

func TestPrettyUnionsColumnsFromEveryRow(t *testing.T) {
	got := render(t, `[{"id":10,"name":"aa"},{"id":20,"email":"ee"}]`)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "id  name  email", lines[0])
	assert.Equal(t, "10  aa", strings.TrimRight(lines[1], " "))
	assert.Equal(t, "20"+strings.Repeat(" ", 8)+"ee", strings.TrimRight(lines[2], " "))
}

func TestPrettyKeepsNumberLiterals(t *testing.T) {
	got := render(t, `[{"quota":1234567890123456789,"ratio":1e6}]`)
	assert.Contains(t, got, "1234567890123456789")
	assert.Contains(t, got, "1e6")
	assert.NotContains(t, got, "e+")
	assert.NotContains(t, got, "1.2345")
}

func TestPrettyScalarArrayFallsBackToLines(t *testing.T) {
	got := render(t, `[1,2,"三",true,null]`)
	assert.Equal(t, "1\n2\n三\ntrue\nnull\n", got)
}

func TestPrettyFlattensCellNewlines(t *testing.T) {
	got := render(t, `[{"note":"line1\nline2"}]`)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "note", strings.TrimRight(lines[0], " "))
	assert.Equal(t, "line1 line2", strings.TrimRight(lines[1], " "))
}

func TestPrettyEmojiWidth(t *testing.T) {
	got := render(t, `[{"v":"😀"},{"v":"ab"}]`)
	assert.Equal(t, "v \n😀\nab\n", got)
}
