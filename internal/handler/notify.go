package handler

import (
	"context"
	"fmt"
	"log/slog"
	"html"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	notifyStateWaitingForMessage = 1
	notifyStateWaitingForConfirm = 2
)

func (h Handler) NotifyCommandHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	language := "en"

	customer, err := h.customerRepository.FindByTelegramId(ctx, update.Message.From.ID)
	if err != nil {
		slog.Error("Error finding customer", "error", err)
		return
	}

	if customer != nil {
		language = customer.Language
	}

	h.cache.Set(update.Message.From.ID, notifyStateWaitingForMessage)

	message := h.translation.GetText(language, "notify_request_message")

	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    update.Message.Chat.ID,
		Text:      message,
		ParseMode: models.ParseModeHTML,
	})

	if err != nil {
		slog.Error("Error sending notify request message", "error", err)
	}
}

func (h Handler) NotifyMessageHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	// Проверяем что сообщение содержит текст ИЛИ фото
	if update.Message == nil || (update.Message.Text == "" && (update.Message.Photo == nil || len(update.Message.Photo) == 0)) {
		return
	}

	state, exists := h.cache.Get(update.Message.From.ID)
	if !exists || state != notifyStateWaitingForMessage {
		return
	}

	language := "en"
	customer, err := h.customerRepository.FindByTelegramId(ctx, update.Message.From.ID)
	if err != nil {
		slog.Error("Error finding customer", "error", err)
		return
	}

	if customer != nil {
		language = customer.Language
	}

	// Определяем текст сообщения и сохраняем file_id фото если есть
	var broadcastMessage string
	var photoFileID string
	hasPhoto := false

	if update.Message.Photo != nil && len(update.Message.Photo) > 0 {
		// Берем последнее фото (самое большое разрешение)
		photoFileID = update.Message.Photo[len(update.Message.Photo)-1].FileID
		hasPhoto = true
		// Для фото используем Caption как текст
		broadcastMessage = html.EscapeString(update.Message.Caption)

		// Сохраняем file_id фото в кэш
		photoKey := fmt.Sprintf("notify_photo_%d", update.Message.From.ID)
		h.cache.SetString(photoKey, photoFileID)
	} else {
		broadcastMessage = html.EscapeString(update.Message.Text)
	}

	// Формируем превью с указанием что будет фото
	var previewMessage string
	if hasPhoto {
		if broadcastMessage != "" {
			previewMessage = fmt.Sprintf(h.translation.GetText(language, "notify_preview_with_photo"), broadcastMessage)
		} else {
			previewMessage = h.translation.GetText(language, "notify_preview_photo_only")
		}
	} else {
		previewMessage = fmt.Sprintf(h.translation.GetText(language, "notify_preview"), broadcastMessage)
	}

	confirmButton := models.InlineKeyboardButton{
		Text:         h.translation.GetText(language, "notify_confirm_button"),
		CallbackData: CallbackNotifyConfirm,
	}

	cancelButton := models.InlineKeyboardButton{
		Text:         h.translation.GetText(language, "notify_cancel_button"),
		CallbackData: CallbackNotifyCancel,
	}

	keyboard := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{confirmButton, cancelButton},
		},
	}

	h.cache.Set(update.Message.From.ID, notifyStateWaitingForConfirm)

	tempKey := fmt.Sprintf("notify_message_%d", update.Message.From.ID)
	h.cache.SetString(tempKey, broadcastMessage)

	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        previewMessage,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: keyboard,
	})

	if err != nil {
		slog.Error("Error sending notify preview message", "error", err)
	}
}

func (h Handler) NotifyConfirmCallbackHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	_, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})
	if err != nil {
		slog.Error("Error answering callback query", "error", err)
	}

	language := "en"
	customer, err := h.customerRepository.FindByTelegramId(ctx, update.CallbackQuery.From.ID)
	if err != nil {
		slog.Error("Error finding customer", "error", err)
		return
	}

	if customer != nil {
		language = customer.Language
	}

	tempKey := fmt.Sprintf("notify_message_%d", update.CallbackQuery.From.ID)
	broadcastMessage, exists := h.cache.GetString(tempKey)
	if !exists {
		slog.Error("Broadcast message not found in cache")
		return
	}

	// Получаем file_id фото если есть
	photoKey := fmt.Sprintf("notify_photo_%d", update.CallbackQuery.From.ID)
	photoFileID, hasPhoto := h.cache.GetString(photoKey)

	h.cache.DeleteString(tempKey)
	h.cache.DeleteString(photoKey)
	h.cache.Set(update.CallbackQuery.From.ID, 0)

	customers, err := h.customerRepository.FindAllTelegramIds(ctx)
	if err != nil {
		slog.Error("Error getting all customers", "error", err)
		return
	}

	broadcastingMessage := fmt.Sprintf(h.translation.GetText(language, "notify_broadcasting"), len(customers))

	_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
		Text:      broadcastingMessage,
		ParseMode: models.ParseModeHTML,
	})

	if err != nil {
		slog.Error("Error editing message", "error", err)
	}

	successCount := 0
	for _, telegramID := range customers {
		var err error

		if hasPhoto {
			// Отправляем фото с caption
			_, err = b.SendPhoto(ctx, &bot.SendPhotoParams{
				ChatID:    telegramID,
				Photo:     &models.InputFileString{Data: photoFileID},
				Caption:   broadcastMessage,
				ParseMode: models.ParseModeHTML,
			})
		} else {
			// Отправляем только текст
			_, err = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:    telegramID,
				Text:      broadcastMessage,
				ParseMode: models.ParseModeHTML,
			})
		}

		if err != nil {
			slog.Error("Error sending broadcast message", "telegramId", telegramID, "error", err)
		} else {
			successCount++
		}
	}

	completedMessage := fmt.Sprintf(h.translation.GetText(language, "notify_completed"), successCount)

	_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
		Text:      completedMessage,
		ParseMode: models.ParseModeHTML,
	})

	if err != nil {
		slog.Error("Error editing completion message", "error", err)
	}

	slog.Info("Broadcast notification completed", "totalUsers", len(customers), "successCount", successCount)
}

func (h Handler) NotifyCancelCallbackHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	_, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})
	if err != nil {
		slog.Error("Error answering callback query", "error", err)
	}

	language := "en"
	customer, err := h.customerRepository.FindByTelegramId(ctx, update.CallbackQuery.From.ID)
	if err != nil {
		slog.Error("Error finding customer", "error", err)
		return
	}

	if customer != nil {
		language = customer.Language
	}

	tempKey := fmt.Sprintf("notify_message_%d", update.CallbackQuery.From.ID)
	photoKey := fmt.Sprintf("notify_photo_%d", update.CallbackQuery.From.ID)
	h.cache.DeleteString(tempKey)
	h.cache.DeleteString(photoKey)
	h.cache.Set(update.CallbackQuery.From.ID, 0)

	cancelledMessage := h.translation.GetText(language, "notify_cancelled")

	_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
		Text:      cancelledMessage,
		ParseMode: models.ParseModeHTML,
	})

	if err != nil {
		slog.Error("Error editing message", "error", err)
	}

	slog.Info("Broadcast notification cancelled by admin")
}
