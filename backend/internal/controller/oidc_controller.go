package controller

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/pocket-id/pocket-id/backend/internal/common"
	"github.com/pocket-id/pocket-id/backend/internal/utils/cookie"

	"github.com/gin-gonic/gin"
	"github.com/pocket-id/pocket-id/backend/internal/dto"
	"github.com/pocket-id/pocket-id/backend/internal/middleware"
	"github.com/pocket-id/pocket-id/backend/internal/service"
	"github.com/pocket-id/pocket-id/backend/internal/utils"
)

// NewOidcController creates a new controller for OIDC related endpoints
// @Summary OIDC controller
// @Description Initializes all OIDC-related API endpoints for authentication and client management
// @Tags OIDC
func NewOidcController(group *gin.RouterGroup, authMiddleware *middleware.AuthMiddleware, fileSizeLimitMiddleware *middleware.FileSizeLimitMiddleware, oidcService *service.OidcService, jwtService *service.JwtService) {
	oc := &OidcController{oidcService: oidcService, jwtService: jwtService}

	group.POST("/oidc/authorize", authMiddleware.WithAdminNotRequired().Add(), oc.authorizeHandler)
	group.POST("/oidc/authorization-required", authMiddleware.WithAdminNotRequired().Add(), oc.authorizationConfirmationRequiredHandler)

	group.POST("/oidc/token", oc.createTokensHandler)
	group.GET("/oidc/userinfo", oc.userInfoHandler)
	group.POST("/oidc/userinfo", oc.userInfoHandler)
	group.POST("/oidc/end-session", authMiddleware.WithAdminNotRequired().WithSuccessOptional().Add(), oc.EndSessionHandler)
	group.GET("/oidc/end-session", authMiddleware.WithAdminNotRequired().WithSuccessOptional().Add(), oc.EndSessionHandler)
	group.POST("/oidc/introspect", oc.introspectTokenHandler)

	group.GET("/oidc/clients", authMiddleware.Add(), oc.listClientsHandler)
	group.POST("/oidc/clients", authMiddleware.Add(), oc.createClientHandler)
	group.GET("/oidc/clients/:id", authMiddleware.Add(), oc.getClientHandler)
	group.GET("/oidc/clients/:id/meta", oc.getClientMetaDataHandler)
	group.PUT("/oidc/clients/:id", authMiddleware.Add(), oc.updateClientHandler)
	group.DELETE("/oidc/clients/:id", authMiddleware.Add(), oc.deleteClientHandler)

	group.PUT("/oidc/clients/:id/allowed-user-groups", authMiddleware.Add(), oc.updateAllowedUserGroupsHandler)
	group.POST("/oidc/clients/:id/secret", authMiddleware.Add(), oc.createClientSecretHandler)

	group.GET("/oidc/clients/:id/logo", oc.getClientLogoHandler)
	group.DELETE("/oidc/clients/:id/logo", oc.deleteClientLogoHandler)
	group.POST("/oidc/clients/:id/logo", authMiddleware.Add(), fileSizeLimitMiddleware.Add(2<<20), oc.updateClientLogoHandler)

	group.POST("/oidc/device/authorize", oc.deviceAuthorizationHandler)
	group.POST("/oidc/device/verify", authMiddleware.WithAdminNotRequired().Add(), oc.verifyDeviceCodeHandler)
	group.GET("/oidc/device/info", authMiddleware.WithAdminNotRequired().Add(), oc.getDeviceCodeInfoHandler)
}

type OidcController struct {
	oidcService *service.OidcService
	jwtService  *service.JwtService
}

// authorizeHandler godoc
// @Summary Authorize OIDC client
// @Description Start the OIDC authorization process for a client
// @Tags OIDC
// @Accept json
// @Produce json
// @Param request body dto.AuthorizeOidcClientRequestDto true "Authorization request parameters"
// @Success 200 {object} dto.AuthorizeOidcClientResponseDto "Authorization code and callback URL"
// @Security BearerAuth
// @Router /api/oidc/authorize [post]
func (oc *OidcController) authorizeHandler(c *gin.Context) {
	var input dto.AuthorizeOidcClientRequestDto
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(err)
		return
	}

	code, callbackURL, err := oc.oidcService.Authorize(c.Request.Context(), input, c.GetString("userID"), c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		_ = c.Error(err)
		return
	}

	response := dto.AuthorizeOidcClientResponseDto{
		Code:        code,
		CallbackURL: callbackURL,
	}

	c.JSON(http.StatusOK, response)
}

// authorizationConfirmationRequiredHandler godoc
// @Summary Check if authorization confirmation is required
// @Description Check if the user needs to confirm authorization for the client
// @Tags OIDC
// @Accept json
// @Produce json
// @Param request body dto.AuthorizationRequiredDto true "Authorization check parameters"
// @Success 200 {object} object "{ \"authorizationRequired\": true/false }"
// @Security BearerAuth
// @Router /api/oidc/authorization-required [post]
func (oc *OidcController) authorizationConfirmationRequiredHandler(c *gin.Context) {
	var input dto.AuthorizationRequiredDto
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(err)
		return
	}

	hasAuthorizedClient, err := oc.oidcService.HasAuthorizedClient(c.Request.Context(), input.ClientID, c.GetString("userID"), input.Scope)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"authorizationRequired": !hasAuthorizedClient})
}

// createTokensHandler godoc
// @Summary Create OIDC tokens
// @Description Exchange authorization code or refresh token for access tokens
// @Tags OIDC
// @Produce json
// @Param client_id formData string false "Client ID (if not using Basic Auth)"
// @Param client_secret formData string false "Client secret (if not using Basic Auth)"
// @Param code formData string false "Authorization code (required for 'authorization_code' grant)"
// @Param grant_type formData string true "Grant type ('authorization_code' or 'refresh_token')"
// @Param code_verifier formData string false "PKCE code verifier (for authorization_code with PKCE)"
// @Param refresh_token formData string false "Refresh token (required for 'refresh_token' grant)"
// @Success 200 {object} dto.OidcTokenResponseDto "Token response with access_token and optional id_token and refresh_token"
// @Router /api/oidc/token [post]
func (oc *OidcController) createTokensHandler(c *gin.Context) {
	var input dto.OidcCreateTokensDto
	if err := c.ShouldBind(&input); err != nil {
		_ = c.Error(err)
		return
	}

	// Validate that code is provided for authorization_code grant type
	if input.GrantType == service.GrantTypeAuthorizationCode && input.Code == "" {
		_ = c.Error(&common.OidcMissingAuthorizationCodeError{})
		return
	}

	// Validate that refresh_token is provided for refresh_token grant type
	if input.GrantType == service.GrantTypeRefreshToken && input.RefreshToken == "" {
		_ = c.Error(&common.OidcMissingRefreshTokenError{})
		return
	}

	// Client id and secret can also be passed over the Authorization header
	if input.ClientID == "" && input.ClientSecret == "" {
		input.ClientID, input.ClientSecret, _ = c.Request.BasicAuth()
	}

	tokens, err := oc.oidcService.CreateTokens(c.Request.Context(), input)

	switch {
	case errors.Is(err, &common.OidcAuthorizationPendingError{}):
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "authorization_pending",
		})
		return
	case errors.Is(err, &common.OidcSlowDownError{}):
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "slow_down",
		})
		return
	case err != nil:
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, dto.OidcTokenResponseDto{
		AccessToken:  tokens.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(tokens.ExpiresIn.Seconds()),
		IdToken:      tokens.IdToken,      // May be empty
		RefreshToken: tokens.RefreshToken, // May be empty
	})
}

// userInfoHandler godoc
// @Summary Get user information
// @Description Get user information based on the access token
// @Tags OIDC
// @Accept json
// @Produce json
// @Success 200 {object} object "User claims based on requested scopes"
// @Security OAuth2AccessToken
// @Router /api/oidc/userinfo [get]
func (oc *OidcController) userInfoHandler(c *gin.Context) {
	_, authToken, ok := strings.Cut(c.GetHeader("Authorization"), " ")
	if !ok || authToken == "" {
		_ = c.Error(&common.MissingAccessToken{})
		return
	}

	token, err := oc.jwtService.VerifyOauthAccessToken(authToken)
	if err != nil {
		_ = c.Error(err)
		return
	}
	userID, ok := token.Subject()
	if !ok {
		_ = c.Error(&common.TokenInvalidError{})
		return
	}
	clientID, ok := token.Audience()
	if !ok || len(clientID) != 1 {
		_ = c.Error(&common.TokenInvalidError{})
		return
	}
	claims, err := oc.oidcService.GetUserClaimsForClient(c.Request.Context(), userID, clientID[0])
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, claims)
}

// EndSessionHandler godoc
// @Summary End OIDC session
// @Description End user session and handle OIDC logout
// @Tags OIDC
// @Accept application/x-www-form-urlencoded
// @Produce html
// @Param id_token_hint query string false "ID token"
// @Param post_logout_redirect_uri query string false "URL to redirect to after logout"
// @Param state query string false "State parameter to include in the redirect"
// @Success 302 "Redirect to post-logout URL or application logout page"
// @Router /api/oidc/end-session [get]
func (oc *OidcController) EndSessionHandler(c *gin.Context) {
	var input dto.OidcLogoutDto

	// Bind query parameters to the struct
	switch c.Request.Method {
	case http.MethodGet:
		if err := c.ShouldBindQuery(&input); err != nil {
			_ = c.Error(err)
			return
		}
	case http.MethodPost:
		// Bind form parameters to the struct
		if err := c.ShouldBind(&input); err != nil {
			_ = c.Error(err)
			return
		}
	}

	callbackURL, err := oc.oidcService.ValidateEndSession(c.Request.Context(), input, c.GetString("userID"))
	if err != nil {
		// If the validation fails, the user has to confirm the logout manually and doesn't get redirected
		log.Printf("Error getting logout callback URL, the user has to confirm the logout manually: %v", err)
		c.Redirect(http.StatusFound, common.EnvConfig.AppURL+"/logout")
		return
	}

	// The validation was successful, so we can log out and redirect the user to the callback URL without confirmation
	cookie.AddAccessTokenCookie(c, 0, "")

	logoutCallbackURL, _ := url.Parse(callbackURL)
	if input.State != "" {
		q := logoutCallbackURL.Query()
		q.Set("state", input.State)
		logoutCallbackURL.RawQuery = q.Encode()
	}

	c.Redirect(http.StatusFound, logoutCallbackURL.String())
}

// EndSessionHandler godoc (POST method)
// @Summary End OIDC session (POST method)
// @Description End user session and handle OIDC logout using POST
// @Tags OIDC
// @Accept application/x-www-form-urlencoded
// @Produce html
// @Param id_token_hint formData string false "ID token"
// @Param post_logout_redirect_uri formData string false "URL to redirect to after logout"
// @Param state formData string false "State parameter to include in the redirect"
// @Success 302 "Redirect to post-logout URL or application logout page"
// @Router /api/oidc/end-session [post]
func (oc *OidcController) EndSessionHandlerPost(c *gin.Context) {
	// Implementation is the same as GET
}

// introspectToken godoc
// @Summary Introspect OIDC tokens
// @Description Pass an access_token to verify if it is considered valid.
// @Tags OIDC
// @Produce json
// @Param token formData string true "The token to be introspected."
// @Success 200 {object} dto.OidcIntrospectionResponseDto "Response with the introspection result."
// @Router /api/oidc/introspect [post]
func (oc *OidcController) introspectTokenHandler(c *gin.Context) {
	var input dto.OidcIntrospectDto
	if err := c.ShouldBind(&input); err != nil {
		_ = c.Error(err)
		return
	}

	// Client id and secret have to be passed over the Authorization header. This kind of
	// authentication allows us to keep the endpoint protected (since it could be used to
	// find valid tokens) while still allowing it to be used by an application that is
	// supposed to interact with our IdP (since that needs to have a client_id
	// and client_secret anyway).
	clientID, clientSecret, _ := c.Request.BasicAuth()

	response, err := oc.oidcService.IntrospectToken(c.Request.Context(), clientID, clientSecret, input.Token)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, response)
}

// getClientMetaDataHandler godoc
// @Summary Get client metadata
// @Description Get OIDC client metadata for discovery and configuration
// @Tags OIDC
// @Produce json
// @Param id path string true "Client ID"
// @Success 200 {object} dto.OidcClientMetaDataDto "Client metadata"
// @Router /api/oidc/clients/{id}/meta [get]
func (oc *OidcController) getClientMetaDataHandler(c *gin.Context) {
	clientId := c.Param("id")
	client, err := oc.oidcService.GetClient(c.Request.Context(), clientId)
	if err != nil {
		_ = c.Error(err)
		return
	}

	clientDto := dto.OidcClientMetaDataDto{}
	err = dto.MapStruct(client, &clientDto)
	if err == nil {
		c.JSON(http.StatusOK, clientDto)
		return
	}

	_ = c.Error(err)
}

// getClientHandler godoc
// @Summary Get OIDC client
// @Description Get detailed information about an OIDC client
// @Tags OIDC
// @Produce json
// @Param id path string true "Client ID"
// @Success 200 {object} dto.OidcClientWithAllowedUserGroupsDto "Client information"
// @Security BearerAuth
// @Router /api/oidc/clients/{id} [get]
func (oc *OidcController) getClientHandler(c *gin.Context) {
	clientId := c.Param("id")
	client, err := oc.oidcService.GetClient(c.Request.Context(), clientId)
	if err != nil {
		_ = c.Error(err)
		return
	}

	clientDto := dto.OidcClientWithAllowedUserGroupsDto{}
	err = dto.MapStruct(client, &clientDto)
	if err == nil {
		c.JSON(http.StatusOK, clientDto)
		return
	}

	_ = c.Error(err)
}

// listClientsHandler godoc
// @Summary List OIDC clients
// @Description Get a paginated list of OIDC clients with optional search and sorting
// @Tags OIDC
// @Param search query string false "Search term to filter clients by name"
// @Param page query int false "Page number, starting from 1" default(1)
// @Param limit query int false "Number of items per page" default(10)
// @Param sort_column query string false "Column to sort by" default("name")
// @Param sort_direction query string false "Sort direction (asc or desc)" default("asc")
// @Success 200 {object} dto.Paginated[dto.OidcClientWithAllowedGroupsCountDto]
// @Security BearerAuth
// @Router /api/oidc/clients [get]
func (oc *OidcController) listClientsHandler(c *gin.Context) {
	searchTerm := c.Query("search")
	var sortedPaginationRequest utils.SortedPaginationRequest
	if err := c.ShouldBindQuery(&sortedPaginationRequest); err != nil {
		_ = c.Error(err)
		return
	}

	clients, pagination, err := oc.oidcService.ListClients(c.Request.Context(), searchTerm, sortedPaginationRequest)
	if err != nil {
		_ = c.Error(err)
		return
	}

	// Map the user groups to DTOs
	var clientsDto = make([]dto.OidcClientWithAllowedGroupsCountDto, len(clients))
	for i, client := range clients {
		var clientDto dto.OidcClientWithAllowedGroupsCountDto
		if err := dto.MapStruct(client, &clientDto); err != nil {
			_ = c.Error(err)
			return
		}
		clientDto.AllowedUserGroupsCount, err = oc.oidcService.GetAllowedGroupsCountOfClient(c, client.ID)
		if err != nil {
			_ = c.Error(err)
			return
		}
		clientsDto[i] = clientDto
	}

	c.JSON(http.StatusOK, dto.Paginated[dto.OidcClientWithAllowedGroupsCountDto]{
		Data:       clientsDto,
		Pagination: pagination,
	})
}

// createClientHandler godoc
// @Summary Create OIDC client
// @Description Create a new OIDC client
// @Tags OIDC
// @Accept json
// @Produce json
// @Param client body dto.OidcClientCreateDto true "Client information"
// @Success 201 {object} dto.OidcClientWithAllowedUserGroupsDto "Created client"
// @Security BearerAuth
// @Router /api/oidc/clients [post]
func (oc *OidcController) createClientHandler(c *gin.Context) {
	var input dto.OidcClientCreateDto
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(err)
		return
	}

	client, err := oc.oidcService.CreateClient(c.Request.Context(), input, c.GetString("userID"))
	if err != nil {
		_ = c.Error(err)
		return
	}

	var clientDto dto.OidcClientWithAllowedUserGroupsDto
	if err := dto.MapStruct(client, &clientDto); err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusCreated, clientDto)
}

// deleteClientHandler godoc
// @Summary Delete OIDC client
// @Description Delete an OIDC client by ID
// @Tags OIDC
// @Param id path string true "Client ID"
// @Success 204 "No Content"
// @Security BearerAuth
// @Router /api/oidc/clients/{id} [delete]
func (oc *OidcController) deleteClientHandler(c *gin.Context) {
	err := oc.oidcService.DeleteClient(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.Status(http.StatusNoContent)
}

// updateClientHandler godoc
// @Summary Update OIDC client
// @Description Update an existing OIDC client
// @Tags OIDC
// @Accept json
// @Produce json
// @Param id path string true "Client ID"
// @Param client body dto.OidcClientCreateDto true "Client information"
// @Success 200 {object} dto.OidcClientWithAllowedUserGroupsDto "Updated client"
// @Security BearerAuth
// @Router /api/oidc/clients/{id} [put]
func (oc *OidcController) updateClientHandler(c *gin.Context) {
	var input dto.OidcClientCreateDto
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(err)
		return
	}

	client, err := oc.oidcService.UpdateClient(c.Request.Context(), c.Param("id"), input)
	if err != nil {
		_ = c.Error(err)
		return
	}

	var clientDto dto.OidcClientWithAllowedUserGroupsDto
	if err := dto.MapStruct(client, &clientDto); err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, clientDto)
}

// createClientSecretHandler godoc
// @Summary Create client secret
// @Description Generate a new secret for an OIDC client
// @Tags OIDC
// @Produce json
// @Param id path string true "Client ID"
// @Success 200 {object} object "{ \"secret\": \"string\" }"
// @Security BearerAuth
// @Router /api/oidc/clients/{id}/secret [post]
func (oc *OidcController) createClientSecretHandler(c *gin.Context) {
	secret, err := oc.oidcService.CreateClientSecret(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"secret": secret})
}

// getClientLogoHandler godoc
// @Summary Get client logo
// @Description Get the logo image for an OIDC client
// @Tags OIDC
// @Produce image/png
// @Produce image/jpeg
// @Produce image/svg+xml
// @Param id path string true "Client ID"
// @Success 200 {file} binary "Logo image"
// @Router /api/oidc/clients/{id}/logo [get]
func (oc *OidcController) getClientLogoHandler(c *gin.Context) {
	imagePath, mimeType, err := oc.oidcService.GetClientLogo(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.Header("Content-Type", mimeType)
	c.File(imagePath)
}

// updateClientLogoHandler godoc
// @Summary Update client logo
// @Description Upload or update the logo for an OIDC client
// @Tags OIDC
// @Accept multipart/form-data
// @Param id path string true "Client ID"
// @Param file formData file true "Logo image file (PNG, JPG, or SVG, max 2MB)"
// @Success 204 "No Content"
// @Security BearerAuth
// @Router /api/oidc/clients/{id}/logo [post]
func (oc *OidcController) updateClientLogoHandler(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		_ = c.Error(err)
		return
	}

	err = oc.oidcService.UpdateClientLogo(c.Request.Context(), c.Param("id"), file)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.Status(http.StatusNoContent)
}

// deleteClientLogoHandler godoc
// @Summary Delete client logo
// @Description Delete the logo for an OIDC client
// @Tags OIDC
// @Param id path string true "Client ID"
// @Success 204 "No Content"
// @Security BearerAuth
// @Router /api/oidc/clients/{id}/logo [delete]
func (oc *OidcController) deleteClientLogoHandler(c *gin.Context) {
	err := oc.oidcService.DeleteClientLogo(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.Status(http.StatusNoContent)
}

// updateAllowedUserGroupsHandler godoc
// @Summary Update allowed user groups
// @Description Update the user groups allowed to access an OIDC client
// @Tags OIDC
// @Accept json
// @Produce json
// @Param id path string true "Client ID"
// @Param groups body dto.OidcUpdateAllowedUserGroupsDto true "User group IDs"
// @Success 200 {object} dto.OidcClientDto "Updated client"
// @Security BearerAuth
// @Router /api/oidc/clients/{id}/allowed-user-groups [put]
func (oc *OidcController) updateAllowedUserGroupsHandler(c *gin.Context) {
	var input dto.OidcUpdateAllowedUserGroupsDto
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(err)
		return
	}

	oidcClient, err := oc.oidcService.UpdateAllowedUserGroups(c.Request.Context(), c.Param("id"), input)
	if err != nil {
		_ = c.Error(err)
		return
	}

	var oidcClientDto dto.OidcClientDto
	if err := dto.MapStruct(oidcClient, &oidcClientDto); err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, oidcClientDto)
}

func (oc *OidcController) deviceAuthorizationHandler(c *gin.Context) {
	var input dto.OidcDeviceAuthorizationRequestDto
	if err := c.ShouldBind(&input); err != nil {
		_ = c.Error(err)
		return
	}

	// Client id and secret can also be passed over the Authorization header
	if input.ClientID == "" && input.ClientSecret == "" {
		input.ClientID, input.ClientSecret, _ = c.Request.BasicAuth()
	}

	response, err := oc.oidcService.CreateDeviceAuthorization(c.Request.Context(), input)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (oc *OidcController) verifyDeviceCodeHandler(c *gin.Context) {
	userCode := c.Query("code")
	if userCode == "" {
		_ = c.Error(&common.ValidationError{Message: "code is required"})
		return
	}

	// Get IP address and user agent from the request context
	ipAddress := c.ClientIP()
	userAgent := c.Request.UserAgent()

	err := oc.oidcService.VerifyDeviceCode(c.Request.Context(), userCode, c.GetString("userID"), ipAddress, userAgent)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (oc *OidcController) getDeviceCodeInfoHandler(c *gin.Context) {
	userCode := c.Query("code")
	if userCode == "" {
		_ = c.Error(&common.ValidationError{Message: "code is required"})
		return
	}

	deviceCodeInfo, err := oc.oidcService.GetDeviceCodeInfo(c.Request.Context(), userCode, c.GetString("userID"))
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, deviceCodeInfo)
}
