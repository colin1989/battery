package agent

import (
	"github.com/colin1989/battery/logger"
	"github.com/colin1989/battery/net/message"
	"github.com/colin1989/battery/net/packet"
	"github.com/colin1989/battery/util"
	"log/slog"
)

func sendPacket(a *Agent, pendingMessage message.PendingMessage) {
	//payload, err := util.SerializeOrRaw(a.serializer, pm.payload)
	//if err != nil {
	//	payload, err = util.GetErrorPayload(a.serializer, err)
	//	if err != nil {
	//		return nil, err
	//	}
	//}

	payload, _ := util.SerializeOrRaw(a.app.Serializer(), pendingMessage.Payload)
	// construct message and encode
	m := &message.Message{
		Type:  pendingMessage.Typ,
		Data:  payload,
		Route: pendingMessage.Route,
		ID:    pendingMessage.Mid,
		Err:   pendingMessage.Err,
	}

	em, err := a.app.MessageEncoder().Encode(m)
	if err != nil {
		logger.Error("actor send client", slog.String("pid", a.PID()),
			logger.ErrAttr(err))
		return
	}
	p, err := a.app.Encoder().Encode(packet.Data, em)
	if err != nil {
		logger.Error("actor send client", slog.String("pid", a.PID()),
			logger.ErrAttr(err))
		return
	}
	if err := a.send(p); err != nil {
		logger.Error("actor send client", slog.String("pid", a.PID()),
			logger.ErrAttr(err))
	}
}
