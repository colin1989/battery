package actor

// A Process is an interface that defines the base contract for interaction of actors
type Process interface {
	SendUserMessage(pid *PID, envelope *MessageEnvelope)
	SendSystemMessage(pid *PID, envelope *MessageEnvelope)
	Stop(pid *PID)
}

type ProcessActor interface {
	Dead()
}

// The Producer type is a function that creates a new actor
type Producer func() Actor

// Actor is the interface that defines the Receive method.
//
// Receive is sent messages to be processed from the mailbox associated with the instance of the actor
type Actor interface {
	Receive(c Context)
}

type queue interface {
	Push(envelope *MessageEnvelope)
	Pop() (*MessageEnvelope, bool)
}
