using System;
using System.Collections.Generic;
using System.Linq;
using Microsoft.Xna.Framework;
using TShockAPI;
using Terraria;
using TerrariaApi.Server;

namespace ServerCommands
{
    [ApiVersion(2, 1)]
    public class ServerCommandsPlugin : TerrariaPlugin
    {
        public override string Name => "ServerCommands";
        public override Version Version => new Version(1, 0, 0);
        public override string Author => "Panel";
        public override string Description => "Server-safe commands for REST API";

        public ServerCommandsPlugin(Main game) : base(game) { }

        public override void Initialize()
        {
            try
            {
                // Commands where delegate reuse works (no RealPlayer check inside handler)
                RegisterAlias("tp", "tprest", "tpothers");
                RegisterAlias("tpallow", "tpallowrest", "tp.allothers");
                RegisterAlias("pos", "posrest", "tp.allothers");
                RegisterAlias("death", "deathrest", "tp.allothers");
                RegisterAlias("pvpdeath", "pvpdeathrest", "tp.allothers");
                RegisterAlias("buff", "buffrest", "buff");
                RegisterAlias("item", "itemrest", "item");

                // Commands that need player context - custom implementations
                Commands.ChatCommands.Add(new Command("spawnboss", SpawnBossCmd, "spawnbossrest")
                    { AllowServer = true, HelpText = "Spawn boss near player (server-safe)" });
                Commands.ChatCommands.Add(new Command("spawnmob", SpawnMobCmd, "spawnmobrest")
                    { AllowServer = true, HelpText = "Spawn mob near player (server-safe)" });
                Commands.ChatCommands.Add(new Command("tp.home", HomeCmd, "homerest")
                    { AllowServer = true, HelpText = "Teleport to home (server-safe)" });
                Commands.ChatCommands.Add(new Command("tp.spawn", SpawnCmd, "spawnrest")
                    { AllowServer = true, HelpText = "Teleport to spawn (server-safe)" });
                Commands.ChatCommands.Add(new Command("tp.tpothers", TpNpcCmd, "tpnpcrest")
                    { AllowServer = true, HelpText = "Teleport to NPC (server-safe)" });
                Commands.ChatCommands.Add(new Command("tp.tphere", TpHereCmd, "tphererest")
                    { AllowServer = true, HelpText = "Teleport player to target (server-safe)" });
                Commands.ChatCommands.Add(new Command("tp.allothers", TpPosCmd, "tpposrest")
                    { AllowServer = true, HelpText = "Teleport to position (server-safe)" });
                Commands.ChatCommands.Add(new Command("tp.allothers", GrowCmd, "growrest")
                    { AllowServer = true, HelpText = "Grow plants (server-safe)" });
                Commands.ChatCommands.Add(new Command("tp.allothers", PartyCmd, "partyrest")
                    { AllowServer = true, HelpText = "Toggle party hat (server-safe)" });

                Commands.ChatCommands.Add(new Command("setspawn", SetSpawnCmd, "setspawnrest")
                    { AllowServer = true, HelpText = "Set spawn point (server-safe)" });

                Console.WriteLine("[ServerCommands] Registered server-safe commands (AllowServer=true)");
            }
            catch (Exception ex)
            {
                Console.WriteLine("[ServerCommands] Init error: " + ex.Message + "\n" + ex.StackTrace);
            }
        }

        private void RegisterAlias(string origName, string restName, string permission)
        {
            var orig = Commands.ChatCommands.FirstOrDefault(c => c.Names.Contains(origName));
            if (orig != null)
            {
                Commands.ChatCommands.Add(new Command(permission, orig.CommandDelegate, restName)
                    { AllowServer = true, HelpText = orig.HelpText + " (server-safe)" });
                Console.WriteLine("[ServerCommands] Alias /" + restName + " -> /" + origName);
            }
        }

        private TSPlayer FindOnlinePlayer(string name)
        {
            if (!string.IsNullOrEmpty(name))
            {
                var list = TSPlayer.FindByNameOrID(name);
                if (list.Count == 1) return list[0];
            }
            // Return first online real player
            foreach (var p in TShock.Players)
            {
                if (p != null && p.RealPlayer) return p;
            }
            return null;
        }

        private void SpawnBossCmd(CommandArgs args)
        {
            try
            {
                if (args.Parameters.Count < 1) { args.Player.SendErrorMessage("Usage: /spawnbossrest <type> [amount] [player]"); return; }
                int type, amount = 1;
                if (!int.TryParse(args.Parameters[0], out type)) { args.Player.SendErrorMessage("Invalid type"); return; }
                if (args.Parameters.Count > 1) int.TryParse(args.Parameters[1], out amount);
                if (amount < 1) amount = 1;

                var player = args.Parameters.Count > 2 ? FindOnlinePlayer(args.Parameters[2]) : FindOnlinePlayer(null);
                if (player == null) { args.Player.SendErrorMessage("No online player found for spawn location"); return; }

                for (int i = 0; i < amount; i++)
                {
                    NPC.NewNPC(null, (int)player.TPlayer.Center.X, (int)player.TPlayer.Center.Y, type);
                }
                args.Player.SendInfoMessage("Spawned " + amount + " boss/NPC " + type + " near " + player.Name + ".");
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }

        private void SpawnMobCmd(CommandArgs args)
        {
            try
            {
                if (args.Parameters.Count < 2) { args.Player.SendErrorMessage("Usage: /spawnmobrest <type> <amount> [player]"); return; }
                int type, amount;
                if (!int.TryParse(args.Parameters[0], out type)) { args.Player.SendErrorMessage("Invalid type"); return; }
                if (!int.TryParse(args.Parameters[1], out amount) || amount < 1) amount = 1;
                if (amount > 200) amount = 200;

                var player = args.Parameters.Count > 2 ? FindOnlinePlayer(args.Parameters[2]) : FindOnlinePlayer(null);
                if (player == null) { args.Player.SendErrorMessage("No online player found for spawn location"); return; }

                for (int i = 0; i < amount; i++)
                {
                    int ox = Main.rand.Next(-30, 31);
                    int oy = Main.rand.Next(-30, 31);
                    NPC.NewNPC(null, (int)player.TPlayer.Center.X + ox, (int)player.TPlayer.Center.Y + oy, type);
                }
                args.Player.SendInfoMessage("Spawned " + amount + " mob " + type + " near " + player.Name + ".");
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }

        private void HomeCmd(CommandArgs args)
        {
            try
            {
                var player = args.Parameters.Count > 0 ? FindOnlinePlayer(args.Parameters[0]) : FindOnlinePlayer(null);
                if (player == null) { args.Player.SendErrorMessage("Player not found"); return; }

                // Player spawn = bed home position. SpawnX/SpawnY are int fields (not nullable)
                int sx = player.TPlayer.SpawnX;
                int sy = player.TPlayer.SpawnY;
                if (sx <= 0 && sy <= 0) { args.Player.SendErrorMessage(player.Name + " has no home set."); return; }

                player.Teleport(new Vector2(sx * 16 + 8, sy * 16), true, 1);
                args.Player.SendInfoMessage("Teleported " + player.Name + " to their home.");
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }

        private void SpawnCmd(CommandArgs args)
        {
            try
            {
                var player = args.Parameters.Count > 0 ? FindOnlinePlayer(args.Parameters[0]) : FindOnlinePlayer(null);
                if (player == null) { args.Player.SendErrorMessage("Player not found"); return; }

                // World spawn point
                int sx = Main.spawnTileX;
                int sy = Main.spawnTileY;
                player.Teleport(new Vector2(sx * 16 + 8, sy * 16), true, 1);
                args.Player.SendInfoMessage("Teleported " + player.Name + " to spawn.");
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }

        private void TpNpcCmd(CommandArgs args)
        {
            try
            {
                if (args.Parameters.Count < 1) { args.Player.SendErrorMessage("Usage: /tpnpcrest <npcid> [player]"); return; }
                int npcId;
                if (!int.TryParse(args.Parameters[0], out npcId)) { args.Player.SendErrorMessage("Invalid NPC ID"); return; }

                NPC found = null;
                for (int i = 0; i < Main.npc.Length; i++)
                {
                    if (Main.npc[i] != null && Main.npc[i].active && Main.npc[i].type == npcId)
                    { found = Main.npc[i]; break; }
                }
                if (found == null) { args.Player.SendErrorMessage("NPC " + npcId + " not found"); return; }

                var player = args.Parameters.Count > 1 ? FindOnlinePlayer(args.Parameters[1]) : FindOnlinePlayer(null);
                if (player == null) { args.Player.SendErrorMessage("Player not found"); return; }

                player.Teleport(new Vector2(found.Bottom.X, found.Bottom.Y), true, 1);
                args.Player.SendInfoMessage("Teleported " + player.Name + " to NPC " + npcId + ".");
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }

        private void TpHereCmd(CommandArgs args)
        {
            try
            {
                if (args.Parameters.Count < 1) { args.Player.SendErrorMessage("Usage: /tphererest <player> [target]"); return; }
                var source = FindOnlinePlayer(args.Parameters[0]);
                if (source == null) { args.Player.SendErrorMessage("Player not found"); return; }

                var target = args.Parameters.Count > 1 ? FindOnlinePlayer(args.Parameters[1]) : null;
                if (target == null) target = (args.Player.RealPlayer) ? args.Player : FindOnlinePlayer(null);
                if (target == null) { args.Player.SendErrorMessage("Target not found"); return; }

                source.Teleport(target.TPlayer.Bottom, true, 1);
                args.Player.SendInfoMessage("Teleported " + source.Name + " to " + target.Name + ".");
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }

        private void TpPosCmd(CommandArgs args)
        {
            try
            {
                if (args.Parameters.Count < 2) { args.Player.SendErrorMessage("Usage: /tpposrest <x> <y> [player]"); return; }
                int tx, ty;
                if (!int.TryParse(args.Parameters[0], out tx) || !int.TryParse(args.Parameters[1], out ty))
                { args.Player.SendErrorMessage("Invalid coordinates"); return; }

                var player = args.Parameters.Count > 2 ? FindOnlinePlayer(args.Parameters[2]) : FindOnlinePlayer(null);
                if (player == null) { args.Player.SendErrorMessage("Player not found"); return; }

                player.Teleport(new Vector2(tx * 16 + 8, ty * 16), true, 1);
                args.Player.SendInfoMessage("Teleported " + player.Name + " to (" + tx + ", " + ty + ").");
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }

        private void GrowCmd(CommandArgs args)
        {
            try
            {
                var player = args.Parameters.Count > 0 ? FindOnlinePlayer(args.Parameters[0]) : FindOnlinePlayer(null);
                if (player == null) { args.Player.SendErrorMessage("Player not found"); return; }

                // Delegate to original /grow command with a real player context
                var origCmd = Commands.ChatCommands.FirstOrDefault(c => c.Names.Contains("grow"));
                if (origCmd != null)
                {
                    var newArgs = new CommandArgs("", player, args.Parameters);
                    origCmd.CommandDelegate(newArgs);
                    args.Player.SendInfoMessage("Executed /grow for " + player.Name + ".");
                }
                else
                {
                    args.Player.SendErrorMessage("/grow command not found");
                }
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }

        private void PartyCmd(CommandArgs args)
        {
            try
            {
                var player = args.Parameters.Count > 0 ? FindOnlinePlayer(args.Parameters[0]) : FindOnlinePlayer(null);
                if (player == null) { args.Player.SendErrorMessage("Player not found"); return; }

                // Delegate to original /party command with a real player context
                var origCmd = Commands.ChatCommands.FirstOrDefault(c => c.Names.Contains("party"));
                if (origCmd != null)
                {
                    var newArgs = new CommandArgs("", player, new List<string>());
                    origCmd.CommandDelegate(newArgs);
                    args.Player.SendInfoMessage("Executed /party for " + player.Name + ".");
                }
                else
                {
                    args.Player.SendErrorMessage("/party command not found");
                }
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }

        private void SetSpawnCmd(CommandArgs args)
        {
            try
            {
                var player = args.Parameters.Count > 0 ? FindOnlinePlayer(args.Parameters[0]) : FindOnlinePlayer(null);
                if (player == null) { args.Player.SendErrorMessage("Player not found"); return; }

                int tileX = (int)(player.TPlayer.Center.X / 16);
                int tileY = (int)(player.TPlayer.Center.Y / 16);
                Main.spawnTileX = tileX;
                Main.spawnTileY = tileY;
                args.Player.SendInfoMessage("Spawn point set to (" + tileX + ", " + tileY + ").");
            }
            catch (Exception ex) { args.Player.SendErrorMessage("Error: " + ex.Message); }
        }
    }
}
