package media

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// RemoveMediaFiles безопасно удаляет файлы, только если прямо сейчас не идет создание бэкапа
func RemoveMediaFiles(dataDir string, filePaths []string) bool {
	if len(filePaths) == 0 {
		return true
	}

	lockPath := filepath.Join(dataDir, ".media.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		return false
	}
	defer lockFile.Close()

	// Пытаемся взять неблокирующий Shared Lock (LOCK_SH | LOCK_NB)
	fd := int(lockFile.Fd())
	err = syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB)
	if err != nil {
		// Если файл занят процессом бэкапа (EWOULDBLOCK / EAGAIN) — пропускаем удаление
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return false
		}
		return false
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)

	for _, path := range filePaths {
		_ = os.Remove(path)
	}

	return true
}
