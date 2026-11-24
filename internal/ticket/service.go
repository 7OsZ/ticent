package ticket

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) ListMyTickets(userID int64) ([]TicketView, error) {
	return s.repo.ListByUser(userID)
}
