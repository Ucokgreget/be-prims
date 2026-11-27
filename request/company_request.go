package request

type CompanyRequest struct {
	Name           string `json:"name"`
	Code           string `json:"code"`
	StorageQuotaGB *int   `json:"storage_quota_gb"`
	Address        string `json:"address"`
}
