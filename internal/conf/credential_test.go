package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCredential(t *testing.T) {
	t.Run("MarshalJSON", func(t *testing.T) {
		cred := Credential{value: "password"}
		expectedJSON := []byte(`"password"`)
		actualJSON, err := cred.MarshalJSON()
		assert.NoError(t, err)
		assert.Equal(t, expectedJSON, actualJSON)
	})

	t.Run("UnmarshalJSON", func(t *testing.T) {
		expectedCred := Credential{value: "password"}
		jsonData := []byte(`"password"`)
		var actualCred Credential
		err := actualCred.UnmarshalJSON(jsonData)
		assert.NoError(t, err)
		assert.Equal(t, expectedCred, actualCred)
	})

	t.Run("UnmarshalEnv", func(t *testing.T) {
		cred := Credential{}
		err := cred.UnmarshalEnv("", "password")
		assert.NoError(t, err)
		assert.Equal(t, "password", cred.value)
	})

	t.Run("GetValue", func(t *testing.T) {
		cred := Credential{value: "password"}
		actualValue := cred.GetValue()
		assert.Equal(t, "password", actualValue)
	})

	t.Run("IsEmpty", func(t *testing.T) {
		cred := Credential{}
		assert.True(t, cred.IsEmpty())
		assert.False(t, cred.IsHashed())

		cred.value = "password"
		assert.False(t, cred.IsEmpty())
		assert.False(t, cred.IsHashed())
	})

	t.Run("IsSha256", func(t *testing.T) {
		cred := Credential{}
		assert.False(t, cred.IsSha256())
		assert.False(t, cred.IsHashed())

		cred.value = "sha256:j1tsRqDEw9xvq/D7/9tMx6Jh/jMhk3UfjwIB2f1zgMo="
		assert.True(t, cred.IsSha256())
		assert.True(t, cred.IsHashed())

		cred.value = "argon2:$argon2id$v=19$m=65536,t=1," +
			"p=4$WXJGqwIB2qd+pRmxMOw9Dg$X4gvR0ZB2DtQoN8vOnJPR2SeFdUhH9TyVzfV98sfWeE"
		assert.False(t, cred.IsSha256())
		assert.True(t, cred.IsHashed())
	})

	t.Run("IsArgon2", func(t *testing.T) {
		cred := Credential{}
		assert.False(t, cred.IsArgon2())
		assert.False(t, cred.IsHashed())

		cred.value = "sha256:j1tsRqDEw9xvq/D7/9tMx6Jh/jMhk3UfjwIB2f1zgMo="
		assert.False(t, cred.IsArgon2())
		assert.True(t, cred.IsHashed())

		cred.value = "argon2:$argon2id$v=19$m=65536,t=1," +
			"p=4$WXJGqwIB2qd+pRmxMOw9Dg$X4gvR0ZB2DtQoN8vOnJPR2SeFdUhH9TyVzfV98sfWeE"
		assert.True(t, cred.IsArgon2())
		assert.True(t, cred.IsHashed())
	})

	t.Run("Check-plain", func(t *testing.T) {
		cred := Credential{value: "password"}
		assert.True(t, cred.Check("password"))
		assert.False(t, cred.Check("wrongpassword"))
	})

	t.Run("Check-sha256", func(t *testing.T) {
		cred := Credential{value: "password"}
		assert.True(t, cred.Check("password"))
		assert.False(t, cred.Check("wrongpassword"))
	})

	t.Run("Check-sha256", func(t *testing.T) {
		cred := Credential{value: "sha256:rl3rgi4NcZkpAEcacZnQ2VuOfJ0FxAqCRaKB/SwdZoQ="}
		assert.True(t, cred.Check("testuser"))
		assert.False(t, cred.Check("notestuser"))
	})

	t.Run("Check-argon2", func(t *testing.T) {
		cred := Credential{value: "argon2:$argon2id$v=19$m=4096,t=3," +
			"p=1$MTIzNDU2Nzg$Ux/LWeTgJQPyfMMJo1myR64+o8rALHoPmlE1i/TR+58"}
		assert.True(t, cred.Check("testuser"))
		assert.False(t, cred.Check("notestuser"))
	})

	t.Run("validateConfig", func(t *testing.T) {
		tests := []struct {
			name    string
			cred    *Credential
			wantErr bool
		}{
			{
				name:    "Empty credential",
				cred:    &Credential{value: ""},
				wantErr: false,
			},
			{
				name:    "Valid plain credential",
				cred:    &Credential{value: "validPlain123"},
				wantErr: false,
			},
			{
				name:    "Invalid plain credential",
				cred:    &Credential{value: "invalid/Plain"},
				wantErr: true,
			},
			{
				name:    "Valid sha256 credential",
				cred:    &Credential{value: "sha256:validBase64EncodedHash=="},
				wantErr: false,
			},
			{
				name:    "Invalid sha256 credential",
				cred:    &Credential{value: "sha256:inval*idBase64"},
				wantErr: true,
			},
			{
				name: "Valid Argon2 credential",
				cred: &Credential{value: "argon2:$argon2id$v=19$m=4096," +
					"t=3,p=1$MTIzNDU2Nzg$zarsL19s86GzUWlAkvwt4gJBFuU/A9CVuCjNI4fksow"},
				wantErr: false,
			},
			{
				name:    "Invalid Argon2 credential",
				cred:    &Credential{value: "argon2:invalid"},
				wantErr: true,
			},
			{
				name: "Invalid Argon2 credential",
				// testing argon2d errors, because it's not supported
				cred: &Credential{value: "$argon2d$v=19$m=4096,t=3," +
					"p=1$MTIzNDU2Nzg$Xqyd4R7LzXvvAEHaVU12+Nzf5OkHoYcwIEIIYJUDpz0"},
				wantErr: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := tt.cred.validateConfig()
				if tt.wantErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})
}
