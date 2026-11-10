package store

import "context"

func MessageGauge(ctx context.Context, db DB) (map[[2]string]int, error) {
	rows, err := db.Query(ctx, `SELECT channel::text,
			CASE WHEN state = 'queued' AND attempts > 0 THEN 'retrying' ELSE state::text END, count(*)
		FROM messages GROUP BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]string]int{}
	for rows.Next() {
		var k [2]string
		var n int
		if err := rows.Scan(&k[0], &k[1], &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

func QueueGauge(ctx context.Context, db DB) (jobs map[[2]string]int, oldest map[string]float64, err error) {
	rows, err := db.Query(ctx, `SELECT queue, state::text, count(*),
			extract(epoch FROM now() - min(created_at))::float8
		FROM river_job GROUP BY 1, 2`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	jobs, oldest = map[[2]string]int{}, map[string]float64{}
	for rows.Next() {
		var k [2]string
		var n int
		var age float64
		if err := rows.Scan(&k[0], &k[1], &n, &age); err != nil {
			return nil, nil, err
		}
		jobs[k] = n
		if k[1] == "available" {
			oldest[k[0]] = age
		}
	}
	return jobs, oldest, rows.Err()
}
