using System;
using System.Collections.Generic;
using Microsoft.Xna.Framework;
using TShockAPI;
using Terraria;
using TerrariaApi.Server;

namespace TeleportRest
{
    [ApiVersion(2, 1)]
    public class TeleportRestPlugin : TerrariaPlugin
    {
        public override string Name => "TeleportRest";
        public override Version Version => new Version(1, 1, 0);
        public override string Author => "Panel";
        public override string Description => "Server-safe teleport command for REST API";

        public TeleportRestPlugin(Main game) : base(game) { }

        public override void Initialize()
        {
            try
            {
                Commands.ChatCommands.Add(new Command("tpothers", TpRestCmd, "tprest")
                {
                    AllowServer = true,
                    HelpText = "Teleports a player (server-safe, for REST API)"
                });

                Console.WriteLine("[TeleportRest] Registered /tprest command (AllowServer=true)");
            }
            catch (Exception ex)
            {
                Console.WriteLine("[TeleportRest] Init error: " + ex.Message + "\n" + ex.StackTrace);
            }
        }

        private void TpRestCmd(CommandArgs args)
        {
            try
            {
                if (args.Parameters.Count < 1)
                {
                    args.Player.SendErrorMessage("Usage: /tprest <from> <to> or /tprest <to>");
                    return;
                }

                string fromName = null;
                string toName = null;

                if (args.Parameters.Count >= 2)
                {
                    fromName = args.Parameters[0];
                    toName = args.Parameters[1];
                }
                else
                {
                    toName = args.Parameters[0];
                }

                var toList = TSPlayer.FindByNameOrID(toName);
                if (toList.Count != 1)
                {
                    args.Player.SendErrorMessage("Target player not found or ambiguous: " + toName);
                    return;
                }
                TSPlayer target = toList[0];

                TSPlayer source;
                if (fromName != null)
                {
                    var fromList = TSPlayer.FindByNameOrID(fromName);
                    if (fromList.Count != 1)
                    {
                        args.Player.SendErrorMessage("Source player not found or ambiguous: " + fromName);
                        return;
                    }
                    source = fromList[0];
                }
                else
                {
                    source = args.Player;
                }

                source.Teleport(target.TPlayer.Bottom, true, 1);

                source.SendInfoMessage("You were teleported to " + target.Name + ".");
                args.Player.SendInfoMessage("Teleported " + source.Name + " to " + target.Name + ".");
            }
            catch (Exception ex)
            {
                args.Player.SendErrorMessage("Teleport error: " + ex.Message);
            }
        }
    }
}
