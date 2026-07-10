package ownership

type Owner struct {
	AccountID string
	UserID    string
	KeyID     *string
}

func (o Owner) Account() string {
	if o.AccountID != "" {
		return o.AccountID
	}
	return o.UserID
}
