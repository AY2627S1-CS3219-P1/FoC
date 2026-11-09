package order

func NewServerWithProgress(
	operations Operations,
	creator CreationOperations,
	images ImageOperations,
	progress ProgressOperations,
	assignments ...AssignmentOperations,
) *Server {
	s := NewServer(operations, creator, images, assignments...)
	s.progress = progress
	return s
}
