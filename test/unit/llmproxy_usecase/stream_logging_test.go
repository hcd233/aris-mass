package llmproxy_usecase

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/samber/lo"

	"github.com/hcd233/aris-mass/internal/application/llmproxy/port"
	"github.com/hcd233/aris-mass/internal/application/llmproxy/usecase"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/dto"
)

type interruptedChatProxy struct{ mockOpenAIProxy }

func (*interruptedChatProxy) ReadChatCompletionStream(context.Context, io.ReadCloser, func(*dto.OpenAIChatCompletionChunk) error) (*dto.OpenAIChatCompletion, error) {
	return nil, io.ErrUnexpectedEOF
}

func TestChatStreamReportsReadFailureAfterWritingErrorFrame(t *testing.T) {
	t.Parallel()

	resolver := &mockResolver{resolveEndpoint: buildTestEndpoint(), resolveModel: buildTestModel()}
	uc := usecase.NewOpenAIUseCase(resolver, &mockListModels{}, &interruptedChatProxy{}, &mockAnthropicProxyForOpenAI{}, &recordingTaskSubmitter{}, nil, nil, nil, nil)
	req := &dto.OpenAIChatCompletionRequest{Body: &dto.OpenAIChatCompletionReq{
		Model: "test-alias", Stream: lo.ToPtr(true),
		Messages: []*dto.OpenAIChatCompletionMessageParam{{Role: enum.RoleUser, Content: &dto.OpenAIMessageContent{Text: "test"}}},
	}}
	result, err := uc.CreateChatCompletion(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	streamResult, ok := result.(*port.StreamResult)
	if !ok {
		t.Fatalf("result = %T, want *port.StreamResult", result)
	}
	stream, err := streamResult.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	sink := &collectSink{}
	if err := stream.Read(t.Context(), sink); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Read returned %v, want io.ErrUnexpectedEOF", err)
	}
	if len(sink.names) == 0 {
		t.Fatal("interrupted stream did not write the protocol error frame")
	}
}
