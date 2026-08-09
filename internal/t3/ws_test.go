package t3

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeFrameArray(t *testing.T) {
	messages, err := decodeFrame([]byte(`[{"_tag":"Exit","requestId":1,"exit":{"_tag":"Success","value":null}}]`))
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "Exit", messages[0].Tag)
	assert.Equal(t, "Success", messages[0].Exit.Tag)
}

// The server sends bare objects as well as arrays, so both must decode.
func TestDecodeFrameSingleObject(t *testing.T) {
	messages, err := decodeFrame([]byte(`{"_tag":"Ping"}`))
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "Ping", messages[0].Tag)
}

func TestDecodeFrameEmpty(t *testing.T) {
	messages, err := decodeFrame([]byte("  "))
	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestDecodeFrameInvalid(t *testing.T) {
	_, err := decodeFrame([]byte(`{not json`))
	assert.Error(t, err)
}

// Request ids come back as numbers or strings depending on the frame, so
// matching must tolerate both rather than assuming one.
func TestMatchesID(t *testing.T) {
	assert.True(t, matchesID(json.RawMessage(`7`), 7))
	assert.True(t, matchesID(json.RawMessage(`"7"`), 7))
	assert.False(t, matchesID(json.RawMessage(`8`), 7))
	assert.False(t, matchesID(json.RawMessage(``), 7))
	assert.False(t, matchesID(json.RawMessage(`null`), 7))
}

// The wire envelope is Effect's RequestEncoded. Field names are load-bearing:
// the method goes in "tag", not "method".
func TestRequestEnvelopeShape(t *testing.T) {
	encoded, err := json.Marshal(wsRequest{
		Tag:     "Request",
		ID:      3,
		Method:  MethodTerminalOpen,
		Payload: map[string]any{"threadId": "t"},
		Headers: [][2]string{},
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, "Request", decoded["_tag"])
	assert.Equal(t, float64(3), decoded["id"])
	assert.Equal(t, "terminal.open", decoded["tag"])
	assert.NotNil(t, decoded["headers"])
}
