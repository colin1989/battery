package actor

type (
	ReceiverFunc func(c ReceiverContext, envelope *MessageEnvelope)
	SenderFunc   func(c SenderContext, target *PID, envelope *MessageEnvelope)
	SpawnFunc    func(actorSystem *ActorSystem, id string, props *Props, parentContext SpawnerContext) (*PID, error)
)

type (
	SenderMiddleware func(next SenderFunc) SenderFunc
	SpawnMiddleware  func(next SpawnFunc) SpawnFunc
)