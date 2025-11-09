import React, { useState, useEffect, useRef } from 'react';

function Comment({comment, setWishlistUpToDate, loggedInUserInfo}) {

    async function deleteComment(commentId) {
        try {
            const response = await fetch('/api/comments', {
                method: 'DELETE',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    "id": commentId,
                })
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            setWishlistUpToDate(false)
        } catch (error) {
            // XXX: handle this?
            console.log('Error deleting comment: ' + error.message);
        }
    }    
    
    var date = new Date(Date.parse(comment.timestamp)).toLocaleString();

    return (<div className="comment-container">
                <div className="comment">
                    <p>
                        { "(" + date + ")" + comment.first + " " + comment.last}
                    </p>
                    <p>
                        {comment.comment}
                    </p>
                </div>
                <div>
                    {loggedInUserInfo["id"] !== comment.user_id ? null : 
                     <button
                         className="comment-delete-button"
                         onClick={() => deleteComment(comment.id)}>
                         Delete Comment
                     </button>
                    }
                </div>
            </div>)
}

function CommentInput({rowId, setWishlistUpToDate}) {
    const [commentInput, setCommentInput] = useState("")
    
    async function postComment() {
        try {
            const response = await fetch('/api/comments', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    "id": rowId,
                    "comment": commentInput,
                })
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            setCommentInput("")
            setWishlistUpToDate(false)
        } catch (error) {
            // XXX: handle this?
            console.log('Error adding comment: ' + error.message);
        }
    }

    return (<div>
                <input
                    type="text"
                    name="comment"
                    value={commentInput}
                    placeholder='Add a comment'
                    onChange={(event) => setCommentInput(event.target.value)}
                />
                {commentInput.length === 0 ? null : 
                 <button onClick={postComment} >
                     Post
                 </button>
                }
            </div>)
}

function ItemComments({rowId, comments, setWishlistUpToDate, loggedInUserInfo}) {

    return (<div>
                {!comments ? null : comments.map((comment, index) => (
                    <Comment key={index}
                             comment={comment}
                             setWishlistUpToDate={setWishlistUpToDate}
                             loggedInUserInfo={loggedInUserInfo}/>
                ))}
                <CommentInput rowId={rowId} setWishlistUpToDate={setWishlistUpToDate}/>
            </div>)
}

function WishlistItems({setIsSignedIn, displayedWishlistUser, wishlistUpToDate,
                        setWishlistUpToDate, loggedInUserInfo}) {
    const [data, setData] = useState(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState(null);
    const [checkedItems, setCheckedItems] = useState([]);
        
    const updateCheckedItems = (itemId, isChecked) => {
        const updatedListOfItems = [...checkedItems];
        if (isChecked) {
            updatedListOfItems.push(itemId);
        } else {
            var idx = updatedListOfItems.indexOf(itemId)
            if (idx !== -1) {
                updatedListOfItems.splice(idx, 1)
            }
        }
        setCheckedItems(updatedListOfItems);
    }
    
    useEffect(() => {
        const fetchData = async () => {
            try {
                var url = '/api/wishlist?' + new URLSearchParams({
                        userId: displayedWishlistUser["id"]
                    }).toString()
                const response = await fetch(url, {
                    headers: {
                        'Content-Type': 'application/json',
                    },
                });

                if (!response.ok) {
                    if (response.status === 401) {
                        // we tried to fetch the wishlist but we're not signed in. Back out
                        setIsSignedIn(false)
                        return
                    }
                    throw new Error(`HTTP error! status: ${response.status}`);
                }
                setData(await response.json());
                setWishlistUpToDate(true)
            } catch (error) {
                setError(error);
            } finally {
                setLoading(false);
            }
        };
        
        fetchData();
    }, [setIsSignedIn, displayedWishlistUser, wishlistUpToDate, setWishlistUpToDate]);

    if (loading) return <p>Loading data...</p>;
    if (error) return <p>Error: {error.message}</p>;

    // Extract column headers from the keys of the first object
    const columns = Object.keys(data["headers"]);

    const isOwner = displayedWishlistUser !== null && displayedWishlistUser["id"] === loggedInUserInfo["id"];

    async function doDelete() {
        try {
            // XXX: there may be stuff in checkedItems may be out of sync with what's actually
            // checked on screen
            
            const response = await fetch('/api/wishlist', {
                method: 'DELETE',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({"ids": checkedItems })
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            setCheckedItems([])
            setWishlistUpToDate(false)
        } catch (error) {
            // XXX: handle this?
            console.log('Error deleting item: ' + error.message);
        }
    }
        
    return (
        <div>
            <h1>{displayedWishlistUser["first"]}'s Wishlist</h1>
                <table>
                    <thead>
                        <tr>
                            {!isOwner ? null : <th/> }
                            {columns.map((column, index) => (
                                <th key={index}>{column}</th>
                            ))}
                        </tr>
                    </thead>
                    <tbody>
                        {data["entries"] === null ? null : data["entries"].map((row, rowIndex) => (
                            <tr key={rowIndex}>
                                {!isOwner ? null :
                                <td>
                                     <input
                                         type="checkbox"
                                         checked={checkedItems.indexOf(row["id"]) !== -1}
                                         onChange={(event) =>
                                             updateCheckedItems(row["id"], event.target.checked)}
                                     />
                                </td>
                                }
                                {columns.map((column, colIndex) => (
                                    column === "comments" ? null : 
                                     <td key={colIndex}>{row[column]}</td>
                                ))}
                                <td>
                                    {isOwner ? null : <ItemComments
                                                          rowId={row["id"]}
                                                          comments={row.comments}
                                                          setWishlistUpToDate={setWishlistUpToDate}
                                                          loggedInUserInfo={loggedInUserInfo}
                                                      />}
                                    
                                 </td>
                            </tr>
                        ))}
                    </tbody>
                </table>
                {!isOwner ? null :
                 <>
                     <button onClick={doDelete} disabled={checkedItems.length === 0}>
                         Delete Selected Items
                     </button>
                     <br/>
                     <button onClick={() => setCheckedItems([])} disabled={checkedItems.length === 0}>
                         Clear Selection
                     </button>
                 </>
                }
                </div>
                );
}

function WishlistAdder({setWishlistUpToDate}) {
    const [formState, setFormState] = useState({
        description: '',
        source: '',
        cost: '',
        owner_notes: ''
    });
    const [postResponse, setPostResponse] = useState('');      

    function handleDescription(event) {
        setFormState(formState => ({
            ...formState,
            description: event.target.value
        }))
    };

    function handleSource(event) {
        setFormState(formState => ({
            ...formState,
            source: event.target.value
        }))
    };

    function handleCost(event) {
        setFormState(formState => ({
            ...formState,
            cost: event.target.value
        }))
    };

    function handleOwnerNotes(event) {
        setFormState(formState => ({
            ...formState,
            owner_notes: event.target.value
        }))
    };

    async function doPost() {
        try {
            const response = await fetch('/api/wishlist', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(formState)
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            await response.json()
            setFormState({
                description: '',
                source: '',
                cost: '',
                owner_notes: ''
            })
            setWishlistUpToDate(false)
        } catch (error) {
            setPostResponse('Error sending data: ' + error.message);
        }
    }
    
    return (
        <div>
            <h1> Add to Wishlist </h1>
            Description <br/>
            <input
                type="text"
                name="description"
                value={formState.description}
                onChange={handleDescription}
            /> <br/>
            Source <br/>
            <input
                type="text"
                name="source"
                value={formState.source}
                onChange={handleSource}
            /> <br/>
            Cost <br/>
            <input
                type="text"
                name="cost"
                value={formState.cost}
                onChange={handleCost}
            /> <br/>            
            Notes <br/>
            <input
                type="text"
                name="notes"
                value={formState.owner_notes}
                onChange={handleOwnerNotes}              
            /> <br/>
            <button onClick={doPost}>
                Add Item
            </button>
            {postResponse && <p>{postResponse}</p>}
        </div>
    );
}


function Login({setShowLogin, setIsSignedIn}) {
    const [email, setEmail] = useState('');
    const [password, setPassword] = useState('');
    const [loginError, setLoginError] = useState('');

    function handleEmail(event) {
        setEmail(event.target.value);
    };

    function handlePassword(event) {
        setPassword(event.target.value);
    };

    async function doLogin() {
        try {
            const response = await fetch('/api/session', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    "email": email,
                    "password": password
                })
            });
            
            const data = await response.text()

            if (!response.ok) {
                setLoginError(data)
            } else {
                setIsSignedIn(true)
            }
        } catch (error) {
            loginError(error.message)
            console.log('Error sending data: ' + error.message);
        }
    }

    return (
        <div>
            <h1> Login </h1>
            Email <br/>
            <input
                type="email"
                name="email"
                onChange={handleEmail}
            /> <br/>
            Password <br/>
            <input
                type="password"
                name="user_password"
                onChange={handlePassword}              
            /> <br/>
            <button onClick={doLogin}>
                Submit
            </button>
            {loginError && <p>{loginError}</p>}
            <p> don't have an account? </p>
            <button onClick={() => setShowLogin(false)}>
                Signup
            </button>            
        </div>
    );
}

function Signup({setShowLogin, setIsSignedIn}) {
    const [formState, setFormState] = useState({
        first: '',
        last: '',
        email: '',
        password: ''
    });
    const formRef = useRef(null);

    function handleFirstName(event) {
        setFormState(formState => ({
            ...formState,
            first: event.target.value
        }))
    };

    function handleLastName(event) {
        setFormState(formState => ({
            ...formState,
            last: event.target.value
        }))
    };

    function handleEmail(event) {
        setFormState(formState => ({
            ...formState,
            email: event.target.value
        }))
    };

    function handlePassword(event) {
        setFormState(formState => ({
            ...formState,
            password: event.target.value
        }))
    };

    async function handleSubmit(event) {
        event.preventDefault();
        if (!formRef.current.checkValidity()) {
            return
        }
        
        try {
            const response = await fetch('/api/signup', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(formState)
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            await response
            setIsSignedIn(true)
        } catch (error) {
            console.log('Error sending data: ' + error.message);
        }
    }

    return (
        <div>
            <h1> Signup </h1>
            First Name <br/>
            <form ref={formRef} onSubmit={handleSubmit}>
                <input
                    type="text"
                    name="firstname"
                    onChange={handleFirstName}
                /> <br/>
                Last Name <br/>
                <input
                    type="text"
                    name="lastname"
                    onChange={handleLastName}
                /> <br/>
                Email <br/>
                <input
                    type="email"
                    name="email"
                    required
                    onChange={handleEmail}
                /> <br/>            
                Password <br/>
                <input
                    type="password"
                    name="user_password"
                    onChange={handlePassword}              
                /> <br/>
                <input type="submit" value="Signup" />             
            </form>
            <p> Already have an account? </p>              
            <button onClick={() => setShowLogin(true)}>
                Login                                 
            </button>                                              
        </div>
    );
}

function LogoutButton({setIsSignedIn}) {
    async function doLogout() {
        try {
            const response = await fetch('/api/session', {
                method: 'DELETE',
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            await response
            setIsSignedIn(false)
        } catch (error) {
            console.log('error logging out' + error.message);
        }
    }

    return (
        <button onClick={doLogout}>
            Logout
        </button>
    );
}

function WishlistSelector({setDisplayedWishlistUser}) {
    const [data, setData] = useState(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState(null);    
    
    useEffect(() => {
        const fetchData = async () => {
            try {
                const response = await fetch('/api/users', {
                    headers: {
                        'Content-Type': 'application/json',
                    },
                });
                if (!response.ok) {
                    throw new Error(`HTTP error! status: ${response.status}`);
                }
                setData(await response.json());
            } catch (error) {
                setError(error);
            } finally {
                setLoading(false);
            }
        };
        
        fetchData();
    }, []);

    if (loading) return <p>Loading data...</p>;
    if (error) return <p>Error: {error.message}</p>;

    return (
        <div>
            <h1>Choose Wishlist Owner</h1>
            <table>
                <tbody>
                    {data["users"] === null ? null : data["users"].map((row, rowIndex) => (
                        <tr key={rowIndex}>
                            <td onClick={() => setDisplayedWishlistUser({
                                    id: row["id"],
                                    first: row["first"],
                                    last: row["last"],
                                }
                                )}
                                style={{ cursor: 'pointer', color: 'blue' }}>
                                {row["first"] + " " + row["last"]}
                            </td>
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    );    
}

function LoginOrSignup({setIsSignedIn}) {
    const [showLogin, setShowLogin] = useState(true);
    return (
        <div>
            {showLogin ? <Login setShowLogin={setShowLogin} setIsSignedIn={setIsSignedIn}/>
             : <Signup setShowLogin={setShowLogin} setIsSignedIn={setIsSignedIn}/>}
        </div>
    );
}

function Wishlist({setIsSignedIn}) {
    const [displayedWishlistUser, setDisplayedWishlistUser] = useState(null)
    const [wishlistUpToDate, setWishlistUpToDate] = useState(true)
    const [loggedInUserInfo, setLoggedInUserInfo] = useState({});
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState(null);        

    useEffect(() => {
        const fetchSession = async () => {
            try {
                // NB: this could probably be cached locally? Maybe the browswer can cache it too, idk
                const response = await fetch('/api/session', {
                    headers: {
                        'Content-Type': 'application/json',
                    },
                });

                if (!response.ok) {
                    if (response.status === 401) {
                        //setIsSignedIn(false)
                        return
                    }
                    throw new Error(`HTTP error! status: ${response.status}`);
                }
                setLoggedInUserInfo(await response.json());
            } catch (error) {
                setError(error);
            } finally {
                setLoading(false);
            }
        };
        
        fetchSession();
    }, [setLoggedInUserInfo]);

    if (loading) return <p>Loading data...</p>;
    if (error) return <p>Error: {error.message}</p>;

    if (displayedWishlistUser === null) {
        setDisplayedWishlistUser(loggedInUserInfo)
    }
    
    return (
        <div>
            <WishlistItems setIsSignedIn={setIsSignedIn}
                           displayedWishlistUser={displayedWishlistUser}
                           wishlistUpToDate={wishlistUpToDate}
                           setWishlistUpToDate={setWishlistUpToDate}
                           loggedInUserInfo={loggedInUserInfo}/>
            {displayedWishlistUser !== null && displayedWishlistUser["id"] === loggedInUserInfo["id"] ? <WishlistAdder setWishlistUpToDate={setWishlistUpToDate}/> : null}
            <WishlistSelector setDisplayedWishlistUser={setDisplayedWishlistUser}/>
            <LogoutButton setIsSignedIn={setIsSignedIn}/>
        </div>
    );
}

export default function MyApp() {
    const [isSignedIn, setIsSignedIn] = useState(true);
                                      
    return (
        <div>
            {isSignedIn ? <Wishlist setIsSignedIn={setIsSignedIn}/> : <LoginOrSignup setIsSignedIn={setIsSignedIn}/>}
        </div>
    );
}
