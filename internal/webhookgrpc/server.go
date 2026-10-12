package webhookgrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "pingmessenger/internal/webhookgrpc/pb"
)

type Handler interface {
	HandleOpenIMWebhook(context.Context, string, []byte) ([]byte, error)
}

type server struct {
	pb.UnimplementedWebhookServer
	handler Handler
}

func (s *server) Call(ctx context.Context, req *pb.CallbackRequest) (*pb.CallbackResponse, error) {
	payload, err := s.handler.HandleOpenIMWebhook(ctx, req.Command, req.Payload)
	if err != nil {
		return &pb.CallbackResponse{ErrCode: 500, ErrMsg: err.Error()}, nil
	}
	return &pb.CallbackResponse{Payload: payload}, nil
}

func Serve(listener grpc.ServiceRegistrar, handler Handler) {
	pb.RegisterWebhookServer(listener, &server{handler: handler})
}

func Dial(ctx context.Context, target string) (*grpc.ClientConn, error) {
	return grpc.DialContext(ctx, target, grpc.WithTransportCredentials(insecure.NewCredentials()))
}
