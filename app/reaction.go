// Copyright (c) 2017-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"net/http"

	"github.com/mattermost/mattermost-server/mlog"
	"github.com/mattermost/mattermost-server/model"
	"github.com/mattermost/mattermost-server/plugin"
)

func (a *App) SaveReactionForPost(reaction *model.Reaction) (*model.Reaction, *model.AppError) {
	post, err := a.GetSinglePost(reaction.PostId)
	if err != nil {
		return nil, err
	}

	channel, err := a.GetChannel(post.ChannelId)
	if err != nil {
		return nil, err
	}

	if channel.DeleteAt > 0 {
		return nil, model.NewAppError("deleteReactionForPost", "api.reaction.save.archived_channel.app_error", nil, "", http.StatusForbidden)
	}

	if a.License() != nil && *a.Config().TeamSettings.ExperimentalTownSquareIsReadOnly && channel.Name == model.DEFAULT_CHANNEL {
		user, err := a.GetUser(reaction.UserId)
		if err != nil {
			return nil, err
		}

		if !a.RolesGrantPermission(user.GetRoles(), model.PERMISSION_MANAGE_SYSTEM.Id) {
			return nil, model.NewAppError("saveReactionForPost", "api.reaction.town_square_read_only", nil, "", http.StatusForbidden)
		}
	}

	result := <-a.Srv.Store.Reaction().Save(reaction)
	if result.Err != nil {
		return nil, result.Err
	}

	reaction = result.Data.(*model.Reaction)

	a.Go(func() {
		a.sendReactionEvent(model.WEBSOCKET_EVENT_REACTION_ADDED, reaction, post, true)
	})

	if a.PluginsReady() {
		a.Go(func() {
			pluginContext := &plugin.Context{}
			mlog.Debug("Reaction has been added")
			a.Plugins.RunMultiPluginHook(func(hooks plugin.Hooks) bool {
				mlog.Debug("Reaction has been sent to plugin")
				hooks.ReactionHasBeenAdded(pluginContext, post, reaction)
				return true
			}, plugin.ReactionHasBeenAddedId)
		})
	}

	return reaction, nil
}

func (a *App) GetReactionsForPost(postId string) ([]*model.Reaction, *model.AppError) {
	result := <-a.Srv.Store.Reaction().GetForPost(postId, true)
	if result.Err != nil {
		return nil, result.Err
	}
	return result.Data.([]*model.Reaction), nil
}

func (a *App) DeleteReactionForPost(reaction *model.Reaction) *model.AppError {
	post, err := a.GetSinglePost(reaction.PostId)
	if err != nil {
		return err
	}

	channel, err := a.GetChannel(post.ChannelId)
	if err != nil {
		return err
	}

	if channel.DeleteAt > 0 {
		return model.NewAppError("deleteReactionForPost", "api.reaction.delete.archived_channel.app_error", nil, "", http.StatusForbidden)
	}

	if a.License() != nil && *a.Config().TeamSettings.ExperimentalTownSquareIsReadOnly && channel.Name == model.DEFAULT_CHANNEL {
		user, err := a.GetUser(reaction.UserId)
		if err != nil {
			return err
		}

		if !a.RolesGrantPermission(user.GetRoles(), model.PERMISSION_MANAGE_SYSTEM.Id) {
			return model.NewAppError("deleteReactionForPost", "api.reaction.town_square_read_only", nil, "", http.StatusForbidden)
		}
	}

	hasReactions := true
	if reactions, _ := a.GetReactionsForPost(post.Id); len(reactions) <= 1 {
		hasReactions = false
	}

	if result := <-a.Srv.Store.Reaction().Delete(reaction); result.Err != nil {
		return result.Err
	}

	a.Go(func() {
		a.sendReactionEvent(model.WEBSOCKET_EVENT_REACTION_REMOVED, reaction, post, hasReactions)
	})

	if a.PluginsReady() {
		a.Go(func() {
			pluginContext := &plugin.Context{}
			mlog.Debug("Reaction has been removed")
			a.Plugins.RunMultiPluginHook(func(hooks plugin.Hooks) bool {
				mlog.Debug("Reaction removal has been sent to plugin")
				hooks.ReactionHasBeenRemoved(pluginContext, post, reaction)
				return true
			}, plugin.ReactionHasBeenRemovedId)
		})
	}

	return nil
}

func (a *App) sendReactionEvent(event string, reaction *model.Reaction, post *model.Post, hasReactions bool) {
	// send out that a reaction has been added/removed
	message := model.NewWebSocketEvent(event, "", post.ChannelId, "", nil)
	message.Add("reaction", reaction.ToJson())
	a.Publish(message)

	// The post is always modified since the UpdateAt always changes
	a.InvalidateCacheForChannelPosts(post.ChannelId)
	post.HasReactions = hasReactions
	post.UpdateAt = model.GetMillis()
	umessage := model.NewWebSocketEvent(model.WEBSOCKET_EVENT_POST_EDITED, "", post.ChannelId, "", nil)
	umessage.Add("post", a.PostWithProxyAddedToImageURLs(post).ToJson())
	a.Publish(umessage)
}
