package model

type BaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

type ModelRemains struct {
	ModelName                   string `json:"model_name"`
	StartTime                   int64  `json:"start_time"`
	EndTime                     int64  `json:"end_time"`
	RemainsTime                 int64  `json:"remains_time"`
	CurrentIntervalTotalCount   int64  `json:"current_interval_total_count"`
	CurrentIntervalUsageCount   int64  `json:"current_interval_usage_count"`
	CurrentIntervalRemainingPct int    `json:"current_interval_remaining_percent"`
	CurrentIntervalStatus       int    `json:"current_interval_status"`
	CurrentWeeklyTotalCount     int64  `json:"current_weekly_total_count"`
	CurrentWeeklyUsageCount     int64  `json:"current_weekly_usage_count"`
	WeeklyStartTime             int64  `json:"weekly_start_time"`
	WeeklyEndTime               int64  `json:"weekly_end_time"`
	WeeklyRemainsTime           int64  `json:"weekly_remains_time"`
	CurrentWeeklyStatus         int    `json:"current_weekly_status"`
	CurrentWeeklyRemainingPct   int    `json:"current_weekly_remaining_percent"`
}

type APIResponse struct {
	ModelRemains []ModelRemains `json:"model_remains"`
	BaseResp     BaseResp       `json:"base_resp"`
}
