package streaming

import (
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
)

// SetSSEHeaders sets the required headers for Server-Sent Events
func SetSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")
	c.Header("X-Accel-Buffering", "no") // Disable nginx buffering
}

// SendSSEData sends a data event in SSE format
func SendSSEData(c *gin.Context, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}

	// SSE format: "data: {json}\n\n"
	_, err = fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
	if err != nil {
		return fmt.Errorf("failed to write SSE data: %w", err)
	}

	// Flush immediately so client receives it
	c.Writer.Flush()
	return nil
}

// SendSSEDone sends the [DONE] message to indicate stream completion
func SendSSEDone(c *gin.Context) error {
	_, err := fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	if err != nil {
		return fmt.Errorf("failed to write SSE done: %w", err)
	}
	c.Writer.Flush()
	return nil
}

// SendSSEError sends an error event in SSE format
func SendSSEError(c *gin.Context, err error) error {
	errorData := map[string]interface{}{
		"error": map[string]interface{}{
			"message": err.Error(),
			"type":    "stream_error",
		},
	}

	jsonData, marshalErr := json.Marshal(errorData)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal error: %w", marshalErr)
	}

	_, writeErr := fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
	if writeErr != nil {
		return fmt.Errorf("failed to write error: %w", writeErr)
	}

	c.Writer.Flush()
	return nil
}

// SendSSEComment sends a comment (for keepalive pings)
func SendSSEComment(c *gin.Context, comment string) error {
	_, err := fmt.Fprintf(c.Writer, ": %s\n\n", comment)
	if err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}
