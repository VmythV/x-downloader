package storage

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var tagColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (database *Database) ListTags(ctx context.Context) ([]Tag, error) {
	rows, err := database.db.QueryContext(ctx, `
SELECT id, name, color, created_at, updated_at
FROM tags ORDER BY normalized_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Tag, 0)
	for rows.Next() {
		var tag Tag
		var createdAt, updatedAt int64
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.Color, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		tag.CreatedAt = timeFromMilliseconds(createdAt)
		tag.UpdatedAt = timeFromMilliseconds(updatedAt)
		result = append(result, tag)
	}
	return result, rows.Err()
}

func (database *Database) CreateTag(ctx context.Context, name, color string) (Tag, error) {
	name, normalized, color, err := validateTag(name, color)
	if err != nil {
		return Tag{}, err
	}
	now := time.Now().UTC()
	result, err := database.db.ExecContext(ctx, `
INSERT INTO tags(name, normalized_name, color, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)`, name, normalized, color, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return Tag{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Tag{}, err
	}
	return Tag{ID: id, Name: name, Color: color, CreatedAt: now, UpdatedAt: now}, nil
}

func (database *Database) UpdateTag(ctx context.Context, id int64, name, color string) (Tag, error) {
	name, normalized, color, err := validateTag(name, color)
	if err != nil {
		return Tag{}, err
	}
	now := time.Now().UTC()
	result, err := database.db.ExecContext(ctx, `
UPDATE tags SET name = ?, normalized_name = ?, color = ?, updated_at = ? WHERE id = ?`,
		name, normalized, color, now.UnixMilli(), id)
	if err != nil {
		return Tag{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Tag{}, errors.New("tag not found")
	}
	var createdAt int64
	if err := database.db.QueryRowContext(ctx, "SELECT created_at FROM tags WHERE id = ?", id).Scan(&createdAt); err != nil {
		return Tag{}, err
	}
	return Tag{ID: id, Name: name, Color: color, CreatedAt: timeFromMilliseconds(createdAt), UpdatedAt: now}, nil
}

func (database *Database) DeleteTag(ctx context.Context, id int64) error {
	result, err := database.db.ExecContext(ctx, "DELETE FROM tags WHERE id = ?", id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("tag not found")
	}
	return nil
}

func (database *Database) AssignTag(ctx context.Context, historyItemID, tagID int64) error {
	_, err := database.db.ExecContext(ctx, `
INSERT INTO library_item_tags(library_item_id, tag_id, created_at)
VALUES (?, ?, ?)
ON CONFLICT(library_item_id, tag_id) DO NOTHING`, historyItemID, tagID, time.Now().UTC().UnixMilli())
	return err
}

func (database *Database) RemoveTag(ctx context.Context, historyItemID, tagID int64) error {
	_, err := database.db.ExecContext(ctx, `
DELETE FROM library_item_tags WHERE library_item_id = ? AND tag_id = ?`, historyItemID, tagID)
	return err
}

func (database *Database) TagsForHistoryItem(ctx context.Context, historyItemID int64) ([]Tag, error) {
	rows, err := database.db.QueryContext(ctx, `
SELECT t.id, t.name, t.color, t.created_at, t.updated_at
FROM tags t
JOIN library_item_tags lit ON lit.tag_id = t.id
WHERE lit.library_item_id = ?
ORDER BY t.normalized_name`, historyItemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Tag, 0)
	for rows.Next() {
		var tag Tag
		var createdAt, updatedAt int64
		if err := rows.Scan(&tag.ID, &tag.Name, &tag.Color, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		tag.CreatedAt = timeFromMilliseconds(createdAt)
		tag.UpdatedAt = timeFromMilliseconds(updatedAt)
		result = append(result, tag)
	}
	return result, rows.Err()
}

func validateTag(name, color string) (string, string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return "", "", "", errors.New("tag name must contain 1 to 64 characters")
	}
	normalized := strings.ToLower(name)
	if color == "" {
		color = "#1d9bf0"
	}
	if !tagColorPattern.MatchString(color) {
		return "", "", "", errors.New("tag color must use #RRGGBB format")
	}
	return name, normalized, strings.ToLower(color), nil
}
