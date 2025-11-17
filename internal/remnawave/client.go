package remnawave

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"remnawave-tg-shop-bot/internal/config"
	"remnawave-tg-shop-bot/utils"
	"strconv"
	"strings"
	"time"

	remapi "github.com/Jolymmiles/remnawave-api-go/v2/api"
	"github.com/google/uuid"
)

type Client struct {
	client *remapi.ClientExt
}

type headerTransport struct {
	base    http.RoundTripper
	xApiKey string
	local   bool
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())

	if t.xApiKey != "" {
		r.Header.Set("X-Api-Key", t.xApiKey)
	}

	if t.local {
		r.Header.Set("x-forwarded-for", "127.0.0.1")
		r.Header.Set("x-forwarded-proto", "https")
	}

	return t.base.RoundTrip(r)
}

func NewClient(baseURL, token, mode string) *Client {
	xApiKey := config.GetXApiKey()
	local := mode == "local"

	client := &http.Client{
		Transport: &headerTransport{
			base:    http.DefaultTransport,
			xApiKey: xApiKey,
			local:   local,
		},
	}

	api, err := remapi.NewClient(baseURL, remapi.StaticToken{Token: token}, remapi.WithClient(client))
	if err != nil {
		panic(err)
	}
	return &Client{client: remapi.NewClientExt(api)}
}

func (r *Client) Ping(ctx context.Context) error {
	params := remapi.UsersControllerGetAllUsersParams{
		Size:  remapi.NewOptFloat64(1),
		Start: remapi.NewOptFloat64(0),
	}
	_, err := r.client.UsersControllerGetAllUsers(ctx, params)
	return err
}

func (r *Client) GetUsers(ctx context.Context) (*[]remapi.GetAllUsersResponseDtoResponseUsersItem, error) {
	pager := remapi.NewPaginationHelper(250)
	users := make([]remapi.GetAllUsersResponseDtoResponseUsersItem, 0)

	for {
		params := remapi.UsersControllerGetAllUsersParams{
			Start: remapi.NewOptFloat64(float64(pager.Offset)),
			Size:  remapi.NewOptFloat64(float64(pager.Limit)),
		}

		resp, err := r.client.Users().GetAllUsers(ctx, params)
		if err != nil {
			return nil, err
		}

		response := resp.(*remapi.GetAllUsersResponseDto).GetResponse()
		users = append(users, response.Users...)

		if len(response.Users) < pager.Limit {
			break
		}

		if !pager.NextPage() {
			break
		}
	}

	return &users, nil
}

func (r *Client) DecreaseSubscription(ctx context.Context, telegramId int64, trafficLimit, days int) (*time.Time, error) {
	resp, err := r.client.Users().GetUserByTelegramId(ctx, remapi.UsersControllerGetUserByTelegramIdParams{TelegramId: strconv.FormatInt(telegramId, 10)})
	if err != nil {
		return nil, err
	}

	switch v := resp.(type) {
	case *remapi.UsersControllerGetUserByTelegramIdNotFound:
		return nil, errors.New("user in remnawave not found")
	case *remapi.UsersResponse:
		var existingUser *remapi.UsersResponseResponseItem
		for _, panelUser := range v.GetResponse() {
			if strings.Contains(panelUser.Username, fmt.Sprintf("_%d", telegramId)) {
				existingUser = &panelUser
			}
		}
		if existingUser == nil {
			existingUser = &v.GetResponse()[0]
		}
		updatedUser, err := r.updateUser(ctx, existingUser, trafficLimit, days, false)
		return &updatedUser.ExpireAt, err
	default:
		return nil, errors.New("unknown response type")
	}
}

func (r *Client) CreateOrUpdateUser(ctx context.Context, customerId int64, telegramId int64, trafficLimit int, days int, isTrialUser bool) (*remapi.UserResponseResponse, error) {
	resp, err := r.client.UsersControllerGetUserByTelegramId(ctx, remapi.UsersControllerGetUserByTelegramIdParams{TelegramId: strconv.FormatInt(telegramId, 10)})
	if err != nil {
		return nil, err
	}

	switch v := resp.(type) {

	case *remapi.UsersControllerGetUserByTelegramIdNotFound:
		return r.createUser(ctx, customerId, telegramId, trafficLimit, days, isTrialUser)
	case *remapi.UsersResponse:
		var existingUser *remapi.UsersResponseResponseItem
		for _, panelUser := range v.GetResponse() {
			if strings.Contains(panelUser.Username, fmt.Sprintf("_%d", telegramId)) {
				existingUser = &panelUser
			}
		}
		if existingUser == nil {
			existingUser = &v.GetResponse()[0]
		}
		return r.updateUser(ctx, existingUser, trafficLimit, days, isTrialUser)
	default:
		return nil, errors.New("unknown response type")
	}
}

func (r *Client) updateUser(ctx context.Context, existingUser *remapi.UsersResponseResponseItem, trafficLimit int, days int, isTrialUser bool) (*remapi.UserResponseResponse, error) {

	newExpire := getNewExpire(days, existingUser.ExpireAt)

	userUpdate := &remapi.UpdateUserRequestDto{
		UUID:              remapi.NewOptUUID(existingUser.UUID),
		ExpireAt:          remapi.NewOptDateTime(newExpire),
		Status:            remapi.NewOptUpdateUserRequestDtoStatus(remapi.UpdateUserRequestDtoStatusACTIVE),
		TrafficLimitBytes: remapi.NewOptInt(trafficLimit),
	}
	
	if config.ExternalSquadUUID() != uuid.Nil {
		userUpdate.ExternalSquadUuid = remapi.NewOptNilUUID(config.ExternalSquadUUID())
	}

	tag := config.RemnawaveTag()
	if isTrialUser {
		tag = config.TrialRemnawaveTag()
	}
	if tag != "" {
		userUpdate.Tag = remapi.NewOptNilString(tag)
	}

	var username string
	if ctx.Value("username") != nil {
		username = ctx.Value("username").(string)
		userUpdate.Description = remapi.NewOptNilString(username)
	} else {
		username = ""
	}

	updateUser, err := r.client.UsersControllerUpdateUser(ctx, userUpdate)
	if err != nil {
		return nil, err
	}
	tgid, _ := existingUser.TelegramId.Get()
	slog.Info("updated user", "telegramId", utils.MaskHalf(strconv.Itoa(tgid)), "username", utils.MaskHalf(username), "days", days)
	return &updateUser.(*remapi.UserResponse).Response, nil
}

func (r *Client) createUser(ctx context.Context, customerId int64, telegramId int64, trafficLimit int, days int, isTrialUser bool) (*remapi.UserResponseResponse, error) {
	expireAt := time.Now().UTC().AddDate(0, 0, days)
	username := generateUsername(customerId, telegramId)

	resp, err := r.client.InternalSquadControllerGetInternalSquads(ctx)
	if err != nil {
		return nil, err
	}

	squads := resp.(*remapi.GetInternalSquadsResponseDto).GetResponse()
	
	selectedSquads := config.SquadUUIDs()
	if isTrialUser {
		selectedSquads = config.TrialInternalSquads()
	}
	
	squadId := make([]uuid.UUID, 0, len(selectedSquads))
	for _, squad := range squads.GetInternalSquads() {
		if selectedSquads != nil && len(selectedSquads) > 0 {
			if _, isExist := selectedSquads[squad.UUID]; !isExist {
				continue
			} else {
				squadId = append(squadId, squad.UUID)
			}
		} else {
			squadId = append(squadId, squad.UUID)
		}
	}

	externalSquad := config.ExternalSquadUUID()
	if isTrialUser {
		externalSquad = config.TrialExternalSquadUUID()
	}

	createUserRequestDto := remapi.CreateUserRequestDto{
		Username:             username,
		ActiveInternalSquads: squadId,
		Status:               remapi.NewOptCreateUserRequestDtoStatus(remapi.CreateUserRequestDtoStatusACTIVE),
		TelegramId:           remapi.NewOptNilInt(int(telegramId)),
		ExpireAt:             expireAt,
		TrafficLimitStrategy: remapi.NewOptCreateUserRequestDtoTrafficLimitStrategy(remapi.CreateUserRequestDtoTrafficLimitStrategyMONTH),
		TrafficLimitBytes:    remapi.NewOptInt(trafficLimit),
	}
	if externalSquad != uuid.Nil {
		createUserRequestDto.ExternalSquadUuid = remapi.NewOptNilUUID(externalSquad)
	}
	tag := config.RemnawaveTag()
	if isTrialUser {
		tag = config.TrialRemnawaveTag()
	}
	if tag != "" {
		createUserRequestDto.Tag = remapi.NewOptNilString(tag)
	}

	var tgUsername string
	if ctx.Value("username") != nil {
		tgUsername = ctx.Value("username").(string)
		createUserRequestDto.Description = remapi.NewOptString(ctx.Value("username").(string))
	} else {
		tgUsername = ""
	}

	userCreate, err := r.client.UsersControllerCreateUser(ctx, &createUserRequestDto)
	if err != nil {
		return nil, err
	}
	slog.Info("created user", "telegramId", utils.MaskHalf(strconv.FormatInt(telegramId, 10)), "username", utils.MaskHalf(tgUsername), "days", days)
	return &userCreate.(*remapi.UserResponse).Response, nil
}

func generateUsername(customerId int64, telegramId int64) string {
	return fmt.Sprintf("%d_%d", customerId, telegramId)
}

func getNewExpire(daysToAdd int, currentExpire time.Time) time.Time {
	if daysToAdd <= 0 {
		return time.Now().UTC().AddDate(0, 0, 1)
	}
	if currentExpire.IsZero() {
		return time.Now().UTC().AddDate(0, 0, daysToAdd)
	}

	if currentExpire.Before(time.Now().UTC()) {
		return time.Now().UTC().AddDate(0, 0, daysToAdd)
	}

	return currentExpire.AddDate(0, 0, daysToAdd)
}
