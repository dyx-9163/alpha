package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateCredentialSecretsRejectsWrongCurrentKey(t *testing.T) {
	const (
		oldSecret     = "old-credential-secret-for-validation"
		currentSecret = "wrong-current-secret-for-validation"
	)
	path := filepath.Join(t.TempDir(), "aifar.db")

	oldStore, err := OpenWithSecret(path, oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	server, err := oldStore.SaveServer(Server{
		Name:     "validation-node",
		Host:     "10.0.0.30",
		Username: "root",
		Password: "validation-plaintext",
	})
	if err != nil {
		t.Fatal(err)
	}
	var cipher string
	if err := oldStore.db.QueryRow(`select password from servers where id=?`, server.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(); err != nil {
		t.Fatal(err)
	}

	wrongStore, err := OpenWithSecret(path, currentSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer wrongStore.Close()
	err = wrongStore.ValidateCredentialSecrets()
	if err == nil {
		t.Fatal("credential secret validation unexpectedly succeeded with the wrong current key")
	}
	for _, sensitive := range []string{oldSecret, currentSecret, "validation-plaintext", cipher} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("validation error leaked sensitive value %q: %v", sensitive, err)
		}
	}
}

func TestOpenWithSecretsTreatsWhitespacePreviousSecretAsConfigured(t *testing.T) {
	const (
		previousSecret = "   "
		currentSecret  = "current-credential-secret-for-whitespace-test"
	)
	path := filepath.Join(t.TempDir(), "aifar.db")

	oldStore, err := OpenWithSecret(path, previousSecret)
	if err != nil {
		t.Fatal(err)
	}
	server, err := oldStore.SaveServer(Server{
		Name:     "whitespace-key-node",
		Host:     "10.0.0.31",
		Username: "root",
		Password: "whitespace-key-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(); err != nil {
		t.Fatal(err)
	}

	rotatingStore, err := OpenWithSecrets(path, currentSecret, previousSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer rotatingStore.Close()
	if _, previous := rotatingStore.secretKeys(); len(previous) == 0 {
		t.Fatal("whitespace previous secret was treated as missing")
	}
	got, err := rotatingStore.GetServer(server.ID, true)
	if err != nil {
		t.Fatalf("read with whitespace previous key: %v", err)
	}
	if got.Password != "whitespace-key-password" {
		t.Fatalf("password = %q", got.Password)
	}
}

func TestStoreCloseClearsCredentialKeyMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aifar.db")
	s, err := OpenWithSecrets(path, "current-key-material-for-close-test", "previous-key-material-for-close-test")
	if err != nil {
		t.Fatal(err)
	}

	s.secretKeyMu.RLock()
	currentBacking := s.secretKey
	previousBacking := s.previousSecretKey
	s.secretKeyMu.RUnlock()
	if len(currentBacking) == 0 || len(previousBacking) == 0 {
		t.Fatal("test store did not retain both credential keys")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s.secretKeyMu.RLock()
	current := s.secretKey
	previous := s.previousSecretKey
	s.secretKeyMu.RUnlock()
	if current != nil || previous != nil {
		t.Fatalf("Store keys after Close = current len %d, previous len %d; want nil", len(current), len(previous))
	}
	for index, value := range currentBacking {
		if value != 0 {
			t.Fatalf("current key backing byte %d was not zeroed", index)
		}
	}
	for index, value := range previousBacking {
		if value != 0 {
			t.Fatalf("previous key backing byte %d was not zeroed", index)
		}
	}
}

func TestRotateCredentialSecretsWithoutPreviousKeyLeavesStateUnchanged(t *testing.T) {
	const currentSecret = "current-secret-without-previous-rotation"
	path := filepath.Join(t.TempDir(), "aifar.db")
	s, err := OpenWithSecret(path, currentSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	server, err := s.SaveServer(Server{
		Name:     "current-only-node",
		Host:     "10.0.0.32",
		Username: "root",
		Password: "current-only-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	var beforeCipher string
	if err := s.db.QueryRow(`select password from servers where id=?`, server.ID).Scan(&beforeCipher); err != nil {
		t.Fatal(err)
	}
	beforeCurrent, beforePrevious := s.secretKeys()
	defer zeroSecretKey(beforeCurrent)
	defer zeroSecretKey(beforePrevious)

	if _, err := s.RotateCredentialSecrets(); err == nil {
		t.Fatal("rotation without a previous credential secret unexpectedly succeeded")
	}
	var afterCipher string
	if err := s.db.QueryRow(`select password from servers where id=?`, server.ID).Scan(&afterCipher); err != nil {
		t.Fatal(err)
	}
	if afterCipher != beforeCipher {
		t.Fatal("ciphertext changed when rotation had no previous key")
	}
	afterCurrent, afterPrevious := s.secretKeys()
	defer zeroSecretKey(afterCurrent)
	defer zeroSecretKey(afterPrevious)
	if string(afterCurrent) != string(beforeCurrent) || len(afterPrevious) != 0 {
		t.Fatal("Store key state changed when rotation had no previous key")
	}
}

func TestValidateCredentialSecretsReadOnlyUsesPreviousKeyWithoutWriting(t *testing.T) {
	const (
		oldSecret     = "old-credential-secret-for-read-only-validation"
		currentSecret = "current-credential-secret-for-read-only-validation"
	)
	path := filepath.Join(t.TempDir(), "aifar.db")

	seedStore, err := OpenWithSecret(path, oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := insertCredentialSecretFixtures(t, seedStore)
	if err := seedStore.Close(); err != nil {
		t.Fatal(err)
	}

	readOnly, err := OpenReadOnlyWithSecrets(path, currentSecret, oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	if err := readOnly.ValidateCredentialSecrets(); err != nil {
		t.Fatalf("validate with previous key: %v", err)
	}
	server, err := readOnly.GetServer("fixture-row-servers", true)
	if err != nil {
		t.Fatalf("read server with previous key: %v", err)
	}
	if server.Password != "server-password" || server.PrivateKey != "server-private-key" {
		t.Fatalf("read-only server secrets = password %q, private key %q", server.Password, server.PrivateKey)
	}
	for _, fixture := range fixtures {
		var after string
		query := fmt.Sprintf("select coalesce(%s,'') from %s where id=?", fixture.target.column, fixture.target.table)
		if err := readOnly.db.QueryRow(query, fixture.id).Scan(&after); err != nil {
			t.Fatalf("read %s.%s: %v", fixture.target.table, fixture.target.column, err)
		}
		if after != fixture.cipher {
			t.Fatalf("validation changed %s.%s row %s", fixture.target.table, fixture.target.column, fixture.id)
		}
	}
}

func TestValidateCredentialSecretsScansEveryKnownEncryptedColumn(t *testing.T) {
	const (
		oldSecret     = "old-credential-secret-for-column-validation"
		currentSecret = "current-credential-secret-for-column-validation"
	)
	targets := []secretRotationTarget{
		{table: "servers", column: "password"},
		{table: "servers", column: "private_key"},
		{table: "credentials", column: "secret_cipher"},
		{table: "credential_versions", column: "secret_cipher"},
		{table: "storage_items", column: "secret_key"},
		{table: "nacos_config_revisions", column: "content_cipher"},
	}

	for _, target := range targets {
		t.Run(target.table+"_"+target.column, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "aifar.db")
			seedStore, err := OpenWithSecret(path, oldSecret)
			if err != nil {
				t.Fatal(err)
			}
			fixtures := insertCredentialSecretFixtures(t, seedStore)
			var selected credentialSecretFixture
			for _, fixture := range fixtures {
				if fixture.target == target {
					selected = fixture
					break
				}
			}
			if selected.id == "" {
				t.Fatalf("missing test fixture for %s.%s", target.table, target.column)
			}
			corrupt := encryptedSecretPrefix + "not-a-valid-encrypted-value"
			update := fmt.Sprintf("update %s set %s=? where id=?", target.table, target.column)
			if _, err := seedStore.db.Exec(update, corrupt, selected.id); err != nil {
				t.Fatal(err)
			}
			if err := seedStore.Close(); err != nil {
				t.Fatal(err)
			}

			readOnly, err := OpenReadOnlyWithSecrets(path, currentSecret, oldSecret)
			if err != nil {
				t.Fatal(err)
			}
			defer readOnly.Close()
			err = readOnly.ValidateCredentialSecrets()
			if err == nil {
				t.Fatalf("validation ignored corrupted %s.%s", target.table, target.column)
			}
			if !strings.Contains(err.Error(), target.table+"."+target.column) || !strings.Contains(err.Error(), selected.id) {
				t.Fatalf("validation error lacks safe location context: %v", err)
			}
			for _, sensitive := range []string{oldSecret, currentSecret, selected.plain, selected.cipher, corrupt} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("validation error leaked sensitive value %q: %v", sensitive, err)
				}
			}
		})
	}
}

func TestRotateCredentialSecretsReencryptsKnownSecretColumns(t *testing.T) {
	const (
		oldSecret     = "old-credential-secret-for-rotation"
		currentSecret = "current-credential-secret-for-rotation"
	)
	path := filepath.Join(t.TempDir(), "aifar.db")

	oldStore, err := OpenWithSecret(path, oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	server, err := oldStore.SaveServer(Server{
		Name:       "old-node",
		Host:       "10.0.0.10",
		Username:   "root",
		Password:   "server-password",
		PrivateKey: "server-private-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyServer, err := oldStore.SaveServer(Server{
		Name:     "legacy-node",
		Host:     "10.0.0.11",
		Username: "root",
		Password: "temporary-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldStore.db.Exec(`update servers set password=?, private_key='' where id=?`, "legacy-plaintext", legacyServer.ID); err != nil {
		t.Fatal(err)
	}

	credential, err := oldStore.SaveCredential(Credential{
		Name:   "mysql-root",
		Kind:   "mysql",
		Secret: map[string]string{"password": "credential-v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	credential.Secret = map[string]string{"password": "credential-v2"}
	credential, err = oldStore.SaveCredential(credential)
	if err != nil {
		t.Fatal(err)
	}

	nacosRevision, err := oldStore.SaveNacosConfigRevision(NacosConfigRevision{
		NacosInstanceID: "nacos-1",
		Namespace:       "prod",
		Group:           "DEFAULT_GROUP",
		DataID:          "application.yml",
		Content:         "spring.password=nacos-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	storageItem, err := oldStore.SaveStorageItem(StorageItem{
		InstanceID: "minio-1",
		Kind:       "accessKey",
		Name:       "admin",
		AccessKey:  "admin",
		SecretKey:  "storage-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(); err != nil {
		t.Fatal(err)
	}

	rotatingStore, err := OpenWithSecrets(path, currentSecret, oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	assertRotatedSecretsReadable(t, rotatingStore, server.ID, credential.ID, nacosRevision.ID, storageItem.ID)

	currentServer, err := rotatingStore.SaveServer(Server{
		Name:     "current-node",
		Host:     "10.0.0.12",
		Username: "root",
		Password: "current-only-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	var currentWriteCipher string
	if err := rotatingStore.db.QueryRow(`select password from servers where id=?`, currentServer.ID).Scan(&currentWriteCipher); err != nil {
		t.Fatal(err)
	}
	if _, err := decryptEncryptedSecret(currentWriteCipher, deriveSecretKey(currentSecret)); err != nil {
		t.Fatalf("new write was not encrypted with current key: %v", err)
	}
	if _, err := decryptEncryptedSecret(currentWriteCipher, deriveSecretKey(oldSecret)); err == nil {
		t.Fatal("new write unexpectedly decrypts with previous key")
	}

	rotated, err := rotatingStore.RotateCredentialSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if rotated < 9 {
		t.Fatalf("rotated values = %d, want at least 9", rotated)
	}
	_, previous := rotatingStore.secretKeys()
	if len(previous) != 0 {
		t.Fatal("previous credential key was not cleared after successful rotation")
	}
	assertRotatedSecretsReadable(t, rotatingStore, server.ID, credential.ID, nacosRevision.ID, storageItem.ID)

	var legacyPassword, legacyPrivateKey string
	if err := rotatingStore.db.QueryRow(`select password, private_key from servers where id=?`, legacyServer.ID).Scan(&legacyPassword, &legacyPrivateKey); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(legacyPassword, encryptedSecretPrefix) {
		t.Fatalf("legacy plaintext password was not encrypted: %q", legacyPassword)
	}
	if legacyPrivateKey != "" {
		t.Fatalf("empty legacy private key changed to %q", legacyPrivateKey)
	}
	if plain, err := rotatingStore.decryptSecret(legacyPassword); err != nil || plain != "legacy-plaintext" {
		t.Fatalf("legacy plaintext after rotation = %q, err=%v", plain, err)
	}

	versionCiphers := readCredentialVersionCiphers(t, rotatingStore, credential.ID)
	if len(versionCiphers) != 2 {
		t.Fatalf("credential version ciphers = %d, want 2", len(versionCiphers))
	}
	for _, cipher := range versionCiphers {
		if _, err := decryptEncryptedSecret(cipher, deriveSecretKey(currentSecret)); err != nil {
			t.Fatalf("credential version does not decrypt with current key: %v", err)
		}
		if _, err := decryptEncryptedSecret(cipher, deriveSecretKey(oldSecret)); err == nil {
			t.Fatal("credential version unexpectedly decrypts with old key")
		}
	}
	if err := rotatingStore.Close(); err != nil {
		t.Fatal(err)
	}

	currentOnly, err := OpenWithSecret(path, currentSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := currentOnly.ValidateCredentialSecrets(); err != nil {
		t.Fatalf("current-only validation after rotation: %v", err)
	}
	assertRotatedSecretsReadable(t, currentOnly, server.ID, credential.ID, nacosRevision.ID, storageItem.ID)
	if err := currentOnly.Close(); err != nil {
		t.Fatal(err)
	}

	oldOnly, err := OpenWithSecret(path, oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer oldOnly.Close()
	if _, err := oldOnly.GetServer(server.ID, true); err == nil {
		t.Fatal("rotated server secrets unexpectedly decrypt with old key")
	}
	if _, err := oldOnly.GetCredential(credential.ID, true); err == nil {
		t.Fatal("rotated credential unexpectedly decrypts with old key")
	}
	if _, err := oldOnly.GetNacosConfigRevision(nacosRevision.ID, true); err == nil {
		t.Fatal("rotated Nacos content unexpectedly decrypts with old key")
	}
	storageCipher := readStorageSecretCipher(t, oldOnly, storageItem.ID)
	if _, err := oldOnly.decryptSecret(storageCipher); err == nil {
		t.Fatal("rotated storage secret unexpectedly decrypts with old key")
	}
}

func TestRotateCredentialSecretsRollsBackOnDecryptFailure(t *testing.T) {
	const (
		oldSecret     = "old-credential-secret-for-rollback"
		currentSecret = "current-credential-secret-for-rollback"
	)
	path := filepath.Join(t.TempDir(), "aifar.db")
	oldStore, err := OpenWithSecret(path, oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	server, err := oldStore.SaveServer(Server{
		Name:     "rollback-node",
		Host:     "10.0.0.20",
		Username: "root",
		Password: "must-remain-old-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := oldStore.SaveCredential(Credential{
		Name:   "broken",
		Kind:   "redis",
		Secret: map[string]string{"password": "must-also-remain-old-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	nacosRevision, err := oldStore.SaveNacosConfigRevision(NacosConfigRevision{
		NacosInstanceID: "nacos-rollback",
		Namespace:       "prod",
		Group:           "DEFAULT_GROUP",
		DataID:          "rollback.yml",
		Content:         "must-trigger-rollback-after-earlier-columns",
	})
	if err != nil {
		t.Fatal(err)
	}
	var beforeServerCipher, beforeCredentialCipher string
	if err := oldStore.db.QueryRow(`select password from servers where id=?`, server.ID).Scan(&beforeServerCipher); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.db.QueryRow(`select secret_cipher from credentials where id=?`, credential.ID).Scan(&beforeCredentialCipher); err != nil {
		t.Fatal(err)
	}
	corruptCipher := encryptedSecretPrefix + "corrupted"
	if _, err := oldStore.db.Exec(`update nacos_config_revisions set content_cipher=? where id=?`, corruptCipher, nacosRevision.ID); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(); err != nil {
		t.Fatal(err)
	}

	rotatingStore, err := OpenWithSecrets(path, currentSecret, oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer rotatingStore.Close()
	_, rotationErr := rotatingStore.RotateCredentialSecrets()
	if rotationErr == nil {
		t.Fatal("rotation with corrupted ciphertext unexpectedly succeeded")
	}
	for _, sensitive := range []string{
		oldSecret,
		currentSecret,
		"must-remain-old-key",
		"must-also-remain-old-key",
		"must-trigger-rollback-after-earlier-columns",
		corruptCipher,
		beforeServerCipher,
		beforeCredentialCipher,
	} {
		if strings.Contains(rotationErr.Error(), sensitive) {
			t.Fatalf("rotation error leaked sensitive value %q: %v", sensitive, rotationErr)
		}
	}
	var afterServerCipher, afterCredentialCipher string
	if err := rotatingStore.db.QueryRow(`select password from servers where id=?`, server.ID).Scan(&afterServerCipher); err != nil {
		t.Fatal(err)
	}
	if err := rotatingStore.db.QueryRow(`select secret_cipher from credentials where id=?`, credential.ID).Scan(&afterCredentialCipher); err != nil {
		t.Fatal(err)
	}
	if afterServerCipher != beforeServerCipher {
		t.Fatal("server ciphertext changed despite transaction rollback")
	}
	if afterCredentialCipher != beforeCredentialCipher {
		t.Fatal("credential ciphertext changed despite transaction rollback")
	}
	if _, previous := rotatingStore.secretKeys(); len(previous) == 0 {
		t.Fatal("previous key was cleared after failed rotation")
	}
	got, err := rotatingStore.GetServer(server.ID, true)
	if err != nil {
		t.Fatalf("fallback key is unavailable after failed rotation: %v", err)
	}
	if got.Password != "must-remain-old-key" {
		t.Fatalf("server password after rollback = %q", got.Password)
	}
	if _, err := decryptEncryptedSecret(afterServerCipher, deriveSecretKey(currentSecret)); err == nil {
		t.Fatal("rolled-back server ciphertext unexpectedly decrypts with current key")
	}
}

func assertRotatedSecretsReadable(t *testing.T, s *Store, serverID, credentialID, nacosRevisionID, storageItemID string) {
	t.Helper()
	server, err := s.GetServer(serverID, true)
	if err != nil {
		t.Fatalf("read server secrets: %v", err)
	}
	if server.Password != "server-password" || server.PrivateKey != "server-private-key" {
		t.Fatalf("server secrets = password %q, private key %q", server.Password, server.PrivateKey)
	}
	credential, err := s.GetCredential(credentialID, true)
	if err != nil {
		t.Fatalf("read credential secret: %v", err)
	}
	if credential.Secret["password"] != "credential-v2" {
		t.Fatalf("credential password = %q", credential.Secret["password"])
	}
	revision, err := s.GetNacosConfigRevision(nacosRevisionID, true)
	if err != nil {
		t.Fatalf("read Nacos config: %v", err)
	}
	if revision.Content != "spring.password=nacos-secret" {
		t.Fatalf("Nacos content = %q", revision.Content)
	}
	storageCipher := readStorageSecretCipher(t, s, storageItemID)
	storageSecret, err := s.decryptSecret(storageCipher)
	if err != nil {
		t.Fatalf("read storage secret: %v", err)
	}
	if storageSecret != "storage-secret" {
		t.Fatalf("storage secret = %q", storageSecret)
	}
}

func readStorageSecretCipher(t *testing.T, s *Store, id string) string {
	t.Helper()
	var cipher string
	if err := s.db.QueryRow(`select secret_key from storage_items where id=?`, id).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	return cipher
}

func readCredentialVersionCiphers(t *testing.T, s *Store, credentialID string) []string {
	t.Helper()
	rows, err := s.db.Query(`select secret_cipher from credential_versions where credential_id=? order by version`, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

type credentialSecretFixture struct {
	target secretRotationTarget
	id     string
	plain  string
	cipher string
}

func insertCredentialSecretFixtures(t *testing.T, s *Store) []credentialSecretFixture {
	t.Helper()
	now := time.Now().UTC()
	fixtures := []credentialSecretFixture{
		{target: secretRotationTarget{table: "servers", column: "password"}, id: "fixture-row-servers", plain: "server-password"},
		{target: secretRotationTarget{table: "servers", column: "private_key"}, id: "fixture-row-servers", plain: "server-private-key"},
		{target: secretRotationTarget{table: "credentials", column: "secret_cipher"}, id: "fixture-row-credentials", plain: `{"password":"credential-password"}`},
		{target: secretRotationTarget{table: "credential_versions", column: "secret_cipher"}, id: "fixture-row-credential-version", plain: `{"password":"credential-version-password"}`},
		{target: secretRotationTarget{table: "storage_items", column: "secret_key"}, id: "fixture-row-storage", plain: "storage-secret"},
		{target: secretRotationTarget{table: "nacos_config_revisions", column: "content_cipher"}, id: "fixture-row-nacos", plain: "nacos-content-secret"},
	}
	for index := range fixtures {
		cipher, err := s.encryptSecret(fixtures[index].plain)
		if err != nil {
			t.Fatal(err)
		}
		fixtures[index].cipher = cipher
	}

	if _, err := s.db.Exec(`insert into servers(id,name,host,port,username,auth_type,password,private_key,tags,note,deploy_dir,docker_host,status,last_error,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, fixtures[0].id, "fixture-node", "10.0.0.40", 22, "root", "password", fixtures[0].cipher, fixtures[1].cipher, "", "", "/aifar/apps", "", "unknown", "", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`insert into credentials(id,name,kind,scope,status,secret_cipher,current_version,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?)`, fixtures[2].id, "fixture-credential", "mysql", "global", "active", fixtures[2].cipher, 1, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`insert into credential_versions(id,credential_id,version,secret_cipher,created_at)
		values(?,?,?,?,?)`, fixtures[3].id, fixtures[2].id, 1, fixtures[3].cipher, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`insert into storage_items(id,instance_id,kind,name,secret_key,created_at,updated_at)
		values(?,?,?,?,?,?,?)`, fixtures[4].id, "minio-fixture", "accessKey", "fixture", fixtures[4].cipher, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`insert into nacos_config_revisions(id,nacos_instance_id,namespace,group_name,data_id,content_cipher,content_hash,created_at,published_at)
		values(?,?,?,?,?,?,?,?,?)`, fixtures[5].id, "nacos-fixture", "public", "DEFAULT_GROUP", "fixture.yml", fixtures[5].cipher, "fixture-hash", now, now); err != nil {
		t.Fatal(err)
	}
	return fixtures
}
